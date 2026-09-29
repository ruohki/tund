package tundmcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"tund/internal/api"
	"tund/internal/client"
)

// pendingLogin is a device login polled in the background.
type pendingLogin struct {
	dl   *client.DeviceLogin
	done chan struct{}

	// Set before done is closed.
	status  string // pending | approved | denied | expired | error
	account string
	err     error
}

type LoginIn struct {
	WaitSeconds int  `json:"wait_seconds,omitempty" jsonschema:"wait up to this many seconds (max 120) for the user to approve before returning"`
	OpenBrowser bool `json:"open_browser,omitempty" jsonschema:"open the approval page in the user's browser; only when the user asked for it"`
}

type LoginOut struct {
	Status                  string `json:"status" jsonschema:"already_logged_in, pending, approved, denied, expired or error"`
	Server                  string `json:"server"`
	Account                 string `json:"account,omitempty"`
	UserCode                string `json:"user_code,omitempty" jsonschema:"the code the user must see on the approval page"`
	VerificationURL         string `json:"verification_url,omitempty"`
	VerificationURLComplete string `json:"verification_url_complete,omitempty" jsonschema:"link that opens the approval page with the code filled in; show it to the user"`
	ExpiresAt               string `json:"expires_at,omitempty"`
	BrowserOpened           bool   `json:"browser_opened,omitempty"`
	Message                 string `json:"message"`
}

func (s *Server) pendingLogin() *pendingLogin {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.login == nil {
		return nil
	}
	select {
	case <-s.login.done:
		return nil
	default:
		return s.login
	}
}

func (pl *pendingLogin) out() LoginOut {
	return LoginOut{
		Status:                  "pending",
		Server:                  pl.dl.Server,
		UserCode:                pl.dl.UserCode,
		VerificationURL:         pl.dl.VerificationURL,
		VerificationURLComplete: pl.dl.VerificationURLComplete,
		ExpiresAt:               pl.dl.ExpiresAt.UTC().Format(time.RFC3339),
		Message: fmt.Sprintf("Ask the user to open %s, check that the page shows the code %s and approve (expires in %s). Call login with wait_seconds to wait for the approval.",
			pl.dl.VerificationURLComplete, pl.dl.UserCode, time.Until(pl.dl.ExpiresAt).Round(time.Minute)),
	}
}

func (s *Server) loginTool(ctx context.Context, _ *mcp.CallToolRequest, in LoginIn) (*mcp.CallToolResult, LoginOut, error) {
	out := LoginOut{Server: s.opts.Server}
	if pl := s.pendingLogin(); pl == nil {
		if c, err := s.apiClient(); err == nil {
			me, err := c.Me(ctx)
			switch {
			case err == nil:
				out.Status, out.Account = "already_logged_in", me.Account.Email
				out.Message = fmt.Sprintf("Already logged in to %s as %s.", s.opts.Server, me.Account.Email)
				return textResult("%s", out.Message), out, nil
			case api.StatusOf(err) == http.StatusUnauthorized:
				s.log.Info("saved authtoken rejected, starting a new login")
			default:
				return nil, out, s.apiError(err)
			}
		}
		dl, err := client.StartDeviceLogin(ctx, s.opts.Server, s.opts.HTTPClient)
		if err != nil {
			return nil, out, err
		}
		pl := &pendingLogin{dl: dl, done: make(chan struct{}), status: "pending"}
		s.mu.Lock()
		s.login = pl
		s.mu.Unlock()
		go s.pollLogin(pl)
		if in.OpenBrowser && !s.opts.NoBrowser {
			out.BrowserOpened = dl.OpenBrowser()
		}
	}

	s.mu.Lock()
	pl := s.login
	s.mu.Unlock()
	if wait := min(in.WaitSeconds, 120); wait > 0 {
		select {
		case <-pl.done:
		case <-time.After(time.Duration(wait) * time.Second):
		case <-ctx.Done():
			return nil, out, ctx.Err()
		}
	}

	select {
	case <-pl.done:
		out.Status, out.Account = pl.status, pl.account
		switch pl.status {
		case "approved":
			out.Message = fmt.Sprintf("Logged in to %s as %s. The token is saved for later sessions.", pl.dl.Server, pl.account)
		case "denied":
			out.Message = "The user denied the login in the browser."
		case "expired":
			out.Message = "The login code expired before it was approved; call login again for a new code."
		default:
			out.Message = fmt.Sprintf("Login failed: %v", pl.err)
		}
		s.mu.Lock()
		if s.login == pl && pl.status != "approved" {
			s.login = nil // the next call starts over
		}
		s.mu.Unlock()
		return textResult("%s", out.Message), out, nil
	default:
	}
	lo := pl.out()
	lo.BrowserOpened = out.BrowserOpened
	text := fmt.Sprintf("Login pending. Show the user:\n\n  Open %s\n  and confirm the code %s\n\nThen call login with wait_seconds (e.g. 60) to wait for the approval.", lo.VerificationURLComplete, lo.UserCode)
	if lo.BrowserOpened {
		text = "Opened the approval page in the user's browser.\n" + text
	}
	return textResult("%s", text), lo, nil
}

// pollLogin waits for approval and saves the token like `tund login`.
func (s *Server) pollLogin(pl *pendingLogin) {
	defer close(pl.done)
	res, err := pl.dl.Poll(s.ctx, nil)
	switch {
	case err == nil:
		pl.status, pl.account = "approved", res.Account
		s.mu.Lock()
		s.token = res.Token
		s.mu.Unlock()
		if err := s.saveLogin(res); err != nil {
			s.log.Warn("logged in, but saving the authtoken failed", "error", err)
		}
		s.log.Info("logged in", "server", res.Server, "account", res.Account)
	case errors.Is(err, client.ErrLoginDenied):
		pl.status = "denied"
	case errors.Is(err, client.ErrLoginExpired):
		pl.status = "expired"
	default:
		pl.status, pl.err = "error", err
	}
}

func (s *Server) saveLogin(res *client.LoginResult) error {
	if s.opts.ConfigPath == "" {
		return nil
	}
	cfg, err := client.LoadConfig(s.opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg.Authtoken = res.Token
	if s.opts.ServerSource != client.SourceDefault {
		cfg.Server = res.Server
	}
	return cfg.Save(s.opts.ConfigPath)
}
