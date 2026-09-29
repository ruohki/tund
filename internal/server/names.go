package server

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

var adjectives = []string{
	"amber", "brave", "bright", "calm", "clever", "cosmic", "crisp", "dapper", "eager", "fancy",
	"fuzzy", "gentle", "glad", "golden", "happy", "humble", "jolly", "keen", "lively", "lucky",
	"mellow", "merry", "misty", "noble", "polite", "proud", "quick", "quiet", "rapid", "rustic",
	"shiny", "silent", "silver", "sleek", "snowy", "solid", "sunny", "swift", "tidy", "vivid",
	"warm", "wild", "witty", "zesty", "bold", "cool", "neat", "plucky", "spry", "breezy",
}

var nouns = []string{
	"otter", "falcon", "badger", "heron", "lynx", "panda", "koala", "moose", "raven", "tiger",
	"walrus", "wombat", "yak", "zebra", "beaver", "bison", "coyote", "dolphin", "ferret", "gecko",
	"ibis", "jaguar", "lemur", "marmot", "newt", "orca", "puffin", "quokka", "robin", "salmon",
	"tapir", "urchin", "viper", "weasel", "alpaca", "bobcat", "condor", "dingo", "egret", "finch",
	"gopher", "hare", "iguana", "jackal", "kiwi", "llama", "mink", "narwhal", "osprey", "pelican",
}

func pick(list []string) string {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
	return list[n.Int64()]
}

// randomLabel returns names like "brave-otter-4821".
func randomLabel() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(9000))
	return fmt.Sprintf("%s-%s-%d", pick(adjectives), pick(nouns), 1000+n.Int64())
}
