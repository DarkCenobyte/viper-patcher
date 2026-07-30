package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type module struct {
	Path    string
	Version string
	Main    bool
	Replace *module
}

type spdxDocument struct {
	SPDXVersion       string        `json:"spdxVersion"`
	DataLicense       string        `json:"dataLicense"`
	SPDXID            string        `json:"SPDXID"`
	Name              string        `json:"name"`
	DocumentNamespace string        `json:"documentNamespace"`
	CreationInfo      creationInfo  `json:"creationInfo"`
	Packages          []spdxPackage `json:"packages"`
}

type creationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxPackage struct {
	Name             string `json:"name"`
	SPDXID           string `json:"SPDXID"`
	VersionInfo      string `json:"versionInfo,omitempty"`
	DownloadLocation string `json:"downloadLocation"`
	FilesAnalyzed    bool   `json:"filesAnalyzed"`
	LicenseConcluded string `json:"licenseConcluded"`
	LicenseDeclared  string `json:"licenseDeclared"`
	CopyrightText    string `json:"copyrightText"`
}

func main() {
	output := flag.String("output", "SBOM.spdx.json", "output SPDX JSON path")
	flag.Parse()

	command := exec.Command("go", "list", "-m", "-json", "all")
	raw, err := command.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go list modules: %v\n", err)
		os.Exit(1)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	var modules []module
	for {
		var value module
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode module list: %v\n", err)
			os.Exit(1)
		}
		if value.Replace != nil {
			value.Version = value.Replace.Version
		}
		modules = append(modules, value)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })

	sum := sha256.Sum256(raw)
	document := spdxDocument{
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              "viper-patcher-go-modules",
		DocumentNamespace: "https://github.com/DarkCenobyte/viper-patcher/sbom/" + hex.EncodeToString(sum[:]),
		CreationInfo: creationInfo{
			Created:  time.Now().UTC().Format(time.RFC3339),
			Creators: []string{"Tool: viper-patcher-sbom"},
		},
	}
	for index, value := range modules {
		version := value.Version
		if value.Main && version == "" {
			version = "development"
		}
		document.Packages = append(document.Packages, spdxPackage{
			Name:             value.Path,
			SPDXID:           fmt.Sprintf("SPDXRef-Package-%d", index+1),
			VersionInfo:      version,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			LicenseConcluded: "NOASSERTION",
			LicenseDeclared:  "NOASSERTION",
			CopyrightText:    "NOASSERTION",
		})
	}
	file, err := os.Create(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create SBOM: %v\n", err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(document)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		fmt.Fprintf(os.Stderr, "write SBOM: %v %v\n", writeErr, closeErr)
		os.Exit(1)
	}
}
