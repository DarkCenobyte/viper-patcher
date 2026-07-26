package patchergui

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"

	"github.com/DarkCenobyte/viper-patcher/assets"
)

const (
	maximumExternalLogoSize   = 16 << 20
	maximumExternalLogoWidth  = 4096
	maximumExternalLogoHeight = 4096
	maximumExternalLogoPixels = 16_000_000
)

func loadPatcherLogo(executablePath string) fyne.Resource {
	if executablePath == "" {
		return assets.AppLogo
	}
	path := filepath.Join(filepath.Dir(executablePath), "assets", "logo.png")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximumExternalLogoSize {
		return assets.AppLogo
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return assets.AppLogo
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || !validExternalLogoDimensions(config.Width, config.Height) {
		return assets.AppLogo
	}
	return fyne.NewStaticResource("logo.png", data)
}

func validExternalLogoDimensions(width, height int) bool {
	if width <= 0 || height <= 0 || width > maximumExternalLogoWidth || height > maximumExternalLogoHeight {
		return false
	}
	return uint64(width)*uint64(height) <= maximumExternalLogoPixels
}
