package internal

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

//nolint:gochecknoglobals // lookup tables for name generation
var adjectives = []string{
	"happy", "swift", "brave", "calm", "eager", "gentle", "jolly", "keen",
	"lively", "merry", "noble", "proud", "quiet", "sharp", "wise", "warm",
	"bold", "bright", "clever", "daring", "fair", "grand", "kind", "witty",
}

//nolint:gochecknoglobals // lookup tables for name generation
var animals = []string{
	"otter", "dolphin", "falcon", "panda", "wolf", "fox", "hawk", "owl",
	"tiger", "eagle", "bear", "lion", "lynx", "raven", "crane", "seal",
	"heron", "bison", "koala", "whale", "robin", "finch", "stork", "viper",
}

func generateName() string {
	return randElement(adjectives) + "-" + randElement(animals)
}

func randElement(list []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(list))))
	if err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}

	return list[n.Int64()]
}
