package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"neproto.local/chameleon/internal/windowscandidate"
)

func main() {
	mode := flag.String("mode", "verify", "create or verify the candidate manifest")
	root := flag.String("root", "", "candidate payload root")
	version := flag.String("version", "", "NP/2 release version used by create mode")
	commit := flag.String("commit", "", "40-character Git commit used by create mode")
	flag.Parse()

	if *root == "" {
		fail("candidate root is required")
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fail("candidate root is invalid")
	}

	var manifest windowscandidate.Manifest
	switch *mode {
	case "create":
		manifest, err = windowscandidate.Create(absoluteRoot, *version, *commit)
		if err == nil {
			err = windowscandidate.Write(absoluteRoot, manifest)
		}
		if err == nil {
			manifest, err = windowscandidate.LoadAndVerify(absoluteRoot)
		}
	case "verify":
		manifest, err = windowscandidate.LoadAndVerify(absoluteRoot)
	default:
		fail("candidate mode must be create or verify")
	}
	if err != nil {
		fail(err.Error())
	}

	result := struct {
		Version       string `json:"version"`
		Commit        string `json:"commit"`
		Platform      string `json:"platform"`
		CarrierPolicy string `json:"carrier_policy"`
		Files         int    `json:"files"`
	}{manifest.Version, manifest.Commit, manifest.Platform, manifest.CarrierPolicy, len(manifest.Files)}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail("cannot report candidate verification")
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
