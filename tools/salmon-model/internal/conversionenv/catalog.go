package conversionenv

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

const (
	ToolchainID                 = "llama-convert-hf"
	Version                     = "bf78f543-python3.11-v1"
	UVVersion                   = "0.7.12"
	PythonVersion               = "3.11.13+20250712"
	ConverterCommit             = "bf78f5439ee8e82e367674043303ebf8e92b4805"
	MaximumExtractedBytes int64 = 3 << 30
)

type Artifact struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	URL       string `json:"url"`
	Format    string `json:"format"`
}

type Platform struct {
	OS                      string
	Arch                    string
	UV                      Artifact
	Python                  Artifact
	LockName                string
	MaximumDependencyBytes  int64
	EstimatedInstalledBytes int64
}

var sourceArtifact = Artifact{Name: "llama.cpp-bf78f543.tar.gz", Kind: "converter-source", SizeBytes: 25325521, SHA256: "e8b7e0fa9a105c649e6898db810b609f1ce46636ba597cfb50020356d121be00", URL: "https://github.com/ggml-org/llama.cpp/archive/bf78f5439ee8e82e367674043303ebf8e92b4805.tar.gz", Format: "tar.gz"}

var platforms = []Platform{
	{OS: "windows", Arch: "amd64", UV: Artifact{Name: "uv-x86_64-pc-windows-msvc.zip", Kind: "uv", SizeBytes: 18236887, SHA256: "2cf29c8ffaa2549aa0f86927b2510008e8ca3dcd2100277d86faf437382a371b", URL: "https://github.com/astral-sh/uv/releases/download/0.7.12/uv-x86_64-pc-windows-msvc.zip", Format: "zip"}, Python: Artifact{Name: "cpython-3.11.13+20250712-x86_64-pc-windows-msvc-install_only_stripped.tar.gz", Kind: "python", SizeBytes: 25441116, SHA256: "43a574437fb7e11c439e13d84dd094fa25c741d32f9245c5ffc0e5f9523aafa9", URL: "https://github.com/astral-sh/python-build-standalone/releases/download/20250712/cpython-3.11.13%2B20250712-x86_64-pc-windows-msvc-install_only_stripped.tar.gz", Format: "tar.gz"}, LockName: "x86_64-pc-windows-msvc.txt", MaximumDependencyBytes: 768 << 20, EstimatedInstalledBytes: 2 << 30},
	{OS: "linux", Arch: "amd64", UV: Artifact{Name: "uv-x86_64-unknown-linux-gnu.tar.gz", Kind: "uv", SizeBytes: 17810666, SHA256: "735891fb553d0be129f3aa39dc8e9c4c49aaa76ec17f7dfb6a732e79a714873a", URL: "https://github.com/astral-sh/uv/releases/download/0.7.12/uv-x86_64-unknown-linux-gnu.tar.gz", Format: "tar.gz"}, Python: Artifact{Name: "cpython-3.11.13+20250712-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz", Kind: "python", SizeBytes: 31547820, SHA256: "e50197b0784baaf2d47c8c8773daa4600b2809330829565e9f31e6cfbc657eae", URL: "https://github.com/astral-sh/python-build-standalone/releases/download/20250712/cpython-3.11.13%2B20250712-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz", Format: "tar.gz"}, LockName: "x86_64-unknown-linux-gnu.txt", MaximumDependencyBytes: 1 << 30, EstimatedInstalledBytes: 2500 << 20},
	{OS: "darwin", Arch: "amd64", UV: Artifact{Name: "uv-x86_64-apple-darwin.tar.gz", Kind: "uv", SizeBytes: 17100006, SHA256: "a338354420dba089218c05d4d585e4bcf174a65fe53260592b2af19ceec85835", URL: "https://github.com/astral-sh/uv/releases/download/0.7.12/uv-x86_64-apple-darwin.tar.gz", Format: "tar.gz"}, Python: Artifact{Name: "cpython-3.11.13+20250712-x86_64-apple-darwin-install_only_stripped.tar.gz", Kind: "python", SizeBytes: 18320390, SHA256: "1eec204b5dffad8a430c2380fd14895fad2b47406f6d69e07f00b954ffdb8064", URL: "https://github.com/astral-sh/python-build-standalone/releases/download/20250712/cpython-3.11.13%2B20250712-x86_64-apple-darwin-install_only_stripped.tar.gz", Format: "tar.gz"}, LockName: "x86_64-apple-darwin.txt", MaximumDependencyBytes: 768 << 20, EstimatedInstalledBytes: 2 << 30},
	{OS: "darwin", Arch: "arm64", UV: Artifact{Name: "uv-aarch64-apple-darwin.tar.gz", Kind: "uv", SizeBytes: 15834517, SHA256: "189108cd026c25d40fb086eaaf320aac52c3f7aab63e185bac51305a1576fc7e", URL: "https://github.com/astral-sh/uv/releases/download/0.7.12/uv-aarch64-apple-darwin.tar.gz", Format: "tar.gz"}, Python: Artifact{Name: "cpython-3.11.13+20250712-aarch64-apple-darwin-install_only_stripped.tar.gz", Kind: "python", SizeBytes: 18002131, SHA256: "cb07230fc0946bab64762b2a97cca278c32c0fa4b1cf5c5c3eb848f08757498a", URL: "https://github.com/astral-sh/python-build-standalone/releases/download/20250712/cpython-3.11.13%2B20250712-aarch64-apple-darwin-install_only_stripped.tar.gz", Format: "tar.gz"}, LockName: "aarch64-apple-darwin.txt", MaximumDependencyBytes: 512 << 20, EstimatedInstalledBytes: 1500 << 20},
}

//go:embed locks/*.txt
var lockFiles embed.FS

func platformFor(goos, goarch string) (Platform, error) {
	for _, platform := range platforms {
		if platform.OS == goos && platform.Arch == goarch {
			return platform, nil
		}
	}
	return Platform{}, fmt.Errorf("conversion environment %s has no pinned artifacts for %s/%s", Version, goos, goarch)
}

func lockFor(platform Platform) ([]byte, string, []string, error) {
	content, err := lockFiles.ReadFile("locks/" + platform.LockName)
	if err != nil {
		return nil, "", nil, err
	}
	digest := sha256.Sum256(content)
	packages := []string{}
	pattern := regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+)==([^ \\]+)`)
	for _, match := range pattern.FindAllSubmatch(content, -1) {
		packages = append(packages, string(match[1])+"=="+string(match[2]))
	}
	return content, hex.EncodeToString(digest[:]), packages, nil
}

func validateHTTPS(value string) bool { return strings.HasPrefix(value, "https://") }
