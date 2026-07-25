package patchergui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/assets"
)

func TestLoadPatcherLogoUsesAdjacentPNG(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "patcher.exe")
	assetDirectory := filepath.Join(directory, "assets")
	if err := os.Mkdir(assetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.Set(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDirectory, "logo.png"), encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	resource := loadPatcherLogo(executable)
	if resource.Name() != "logo.png" || bytes.Equal(resource.Content(), assets.AppLogo.Content()) {
		t.Fatal("expected the adjacent logo to override the embedded resource")
	}
}

func TestLoadPatcherLogoFallsBackForInvalidPNG(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "patcher.exe")
	assetDirectory := filepath.Join(directory, "assets")
	if err := os.Mkdir(assetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDirectory, "logo.png"), []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	resource := loadPatcherLogo(executable)
	if !bytes.Equal(resource.Content(), assets.AppLogo.Content()) {
		t.Fatal("expected the embedded logo fallback")
	}
}

func TestLoadPatcherLogoIgnoresLogoOutsideAssetsDirectory(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "patcher.exe")
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "logo.png"), encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	resource := loadPatcherLogo(executable)
	if !bytes.Equal(resource.Content(), assets.AppLogo.Content()) {
		t.Fatal("expected only assets/logo.png to override the embedded logo")
	}
}
