import "server-only";
import { randomInt } from "node:crypto";

// Same word lists as tund-server's generator (internal/server/names.go), so
// names claimed in the dashboard look like the ones the server hands out.
const ADJECTIVES = [
  "amber", "brave", "bright", "calm", "clever", "cosmic", "crisp", "dapper", "eager", "fancy", "fuzzy",
  "gentle", "glad", "golden", "happy", "humble", "jolly", "keen", "lively", "lucky", "mellow", "merry",
  "misty", "noble", "polite", "proud", "quick", "quiet", "rapid", "rustic", "shiny", "silent", "silver",
  "sleek", "snowy", "solid", "sunny", "swift", "tidy", "vivid", "warm", "wild", "witty", "zesty", "bold",
  "cool", "neat", "plucky", "spry", "breezy",
];

const NOUNS = [
  "otter", "falcon", "badger", "heron", "lynx", "panda", "koala", "moose", "raven", "tiger", "walrus",
  "wombat", "yak", "zebra", "beaver", "bison", "coyote", "dolphin", "ferret", "gecko", "ibis", "jaguar",
  "lemur", "marmot", "newt", "orca", "puffin", "quokka", "robin", "salmon", "tapir", "urchin", "viper",
  "weasel", "alpaca", "bobcat", "condor", "dingo", "egret", "finch", "gopher", "hare", "iguana", "jackal",
  "kiwi", "llama", "mink", "narwhal", "osprey", "pelican",
];

/** A label like "brave-otter-4821". */
export function randomLabel(): string {
  return `${ADJECTIVES[randomInt(ADJECTIVES.length)]}-${NOUNS[randomInt(NOUNS.length)]}-${1000 + randomInt(9000)}`;
}
