// Command releasekey prints the Ed25519 public key derived from the signing
// seed. It is used in CI to verify the published public key matches the
// private key; the seed itself is never printed.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	seedHex := flag.String("seed", os.Getenv("ATM_SIGNING_SEED"), "hex Ed25519 seed")
	flag.Parse()
	seed, err := hex.DecodeString(strings.TrimSpace(*seedHex))
	if err != nil || len(seed) != ed25519.SeedSize {
		fmt.Fprintln(os.Stderr, errors.New("签名种子必须是 64 字符十六进制"))
		os.Exit(1)
	}
	private := ed25519.NewKeyFromSeed(seed)
	public := private.Public().(ed25519.PublicKey)
	fmt.Println(hex.EncodeToString(public))
}
