package server

import (
	"context"
	"os"
	"testing"
)

func TestRuntimePlan(t *testing.T) {
	r := &Runtime{
		MaxTunnelsPerUser: 2, MaxPinnedPerUser: 1, MaxDomainsPerUser: 0, TransferGB: 5, TunnelLifetime: 120,
		ProMaxTunnels: 10, ProMaxPinned: 10, ProMaxDomains: 5, ProTransferGB: 50,
	}
	free, pro := r.plan(false), r.plan(true)
	r.MaxTunnelsPerUser = 0 // unlimited on Free stays unlimited on Pro
	if got := r.plan(true).Tunnels; got != 0 {
		t.Errorf("pro tunnels with unlimited free = %d", got)
	}
	if got := r.plan(true).Domains; got != 5 {
		t.Errorf("pro domains with custom domains off on free = %d", got)
	}
	r.MaxTunnelsPerUser, r.ProMaxTunnels = 20, 10 // Pro never lower than Free
	if got := r.plan(true).Tunnels; got != 20 {
		t.Errorf("pro tunnels below free = %d", got)
	}
	if free.Pro || free.Tunnels != 2 || free.CustomDomains || free.Passthrough || free.TunnelLifetime != 120 {
		t.Errorf("free = %+v", free)
	}
	if !pro.Pro || pro.Tunnels != 10 || !pro.CustomDomains || !pro.Passthrough || pro.TransferGB != 50 || pro.TunnelLifetime != 0 {
		t.Errorf("pro = %+v", pro)
	}
}

// Runs against a real database: TUND_TEST_DATABASE_URL=postgres://… go test ./internal/server -run PlansPostgres
func TestPlansPostgres(t *testing.T) {
	dsn := os.Getenv("TUND_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TUND_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := OpenStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close) // registered first, so it runs after the cleanups below
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user := func(email string) string {
		var id string
		if err := st.pool.QueryRow(ctx, `insert into users (email, password_hash) values ($1, 'x') returning id`, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.pool.Exec(ctx, `delete from users where id = $1`, id) })
		return id
	}
	owner, member, other := user("plan-owner@example.com"), user("plan-member@example.com"), user("plan-other@example.com")
	var team string
	if err := st.pool.QueryRow(ctx, `insert into teams (name, slug, created_by) values ('Plan test', 'plan-test', $1) returning id`, owner).Scan(&team); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.pool.Exec(ctx, `delete from subscriptions where customer_id = 'cus_plan_test'`)
		st.pool.Exec(ctx, `delete from teams where id = $1`, team)
	})
	st.pool.Exec(ctx, `insert into team_members (team_id, user_id, role) values ($1, $2, 'owner'), ($1, $3, 'member')`, team, owner, member)

	pro := func(id string) bool {
		t.Helper()
		l, err := st.UserLimits(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return l.Pro
	}
	trusted := func(id string) bool {
		t.Helper()
		ok, err := st.UserTrusted(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	check := func(step string, wantOwner, wantMember, wantOther bool, wantPlan string) {
		t.Helper()
		if got := [3]bool{pro(owner), pro(member), pro(other)}; got != [3]bool{wantOwner, wantMember, wantOther} {
			t.Errorf("%s: pro owner/member/other = %v", step, got)
		}
		if plan, err := st.TeamPlan(ctx, team); err != nil || plan != wantPlan {
			t.Errorf("%s: team plan = %q, %v", step, plan, err)
		}
	}
	sub := func(id, plan, status string, userID, teamID any) {
		if _, err := st.pool.Exec(ctx, `insert into subscriptions (id, customer_id, user_id, team_id, plan, status) values ($1, 'cus_plan_test', $2, $3, $4, $5)
			on conflict (id) do update set status = excluded.status`, id, userID, teamID, plan, status); err != nil {
			t.Fatal(err)
		}
	}

	check("nothing", false, false, false, "")

	st.pool.Exec(ctx, `update users set pro_granted = true where id = $1`, other)
	check("granted", false, false, true, "")
	if trusted(other) {
		t.Error("granted Pro must not make an account trusted")
	}
	st.pool.Exec(ctx, `update users set pro_granted = false where id = $1`, other)

	sub("sub_pro", "pro", "active", other, nil)
	check("pro subscription", false, false, true, "")
	if !trusted(other) {
		t.Error("a paying account must count as trusted")
	}
	sub("sub_pro", "pro", "canceled", other, nil)
	check("pro cancelled", false, false, false, "")
	if trusted(other) {
		t.Error("trust must end with the subscription")
	}

	sub("sub_team", "team", "active", owner, team)
	check("team", true, false, false, "team")
	sub("sub_team", "team", "past_due", owner, team)
	check("team past due (grace)", true, false, false, "team")
	sub("sub_team", "team", "unpaid", owner, team)
	check("team unpaid", false, false, false, "")

	sub("sub_team_pro", "team_pro", "active", owner, team)
	check("team pro", true, true, false, "team_pro")
	sub("sub_team_pro", "team_pro", "canceled", owner, team)

	st.pool.Exec(ctx, `update teams set plan_granted = 'team' where id = $1`, team)
	check("granted team", true, false, false, "team")
	st.pool.Exec(ctx, `update teams set plan_granted = 'team_pro' where id = $1`, team)
	check("granted team pro", true, true, false, "team_pro")
}
