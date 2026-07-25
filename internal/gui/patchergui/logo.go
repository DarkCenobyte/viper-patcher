package patchergui

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"

	"github.com/DarkCenobyte/viper-patcher/assets"
)

const maximumExternalLogoSize = 16 << 20

func loadPatcherLogo(executablePath string) fyne.Resource {
	if executablePath == "" {
		return assets.AppLogo
	}
	path := filepath.Join(filepath.Dir(executablePath), "assets", "logo.png")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maximumExternalLogoSize {
		return assets.AppLogo
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return assets.AppLogo
	}
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return assets.AppLogo
	}
	return fyne.NewStaticResource("logo.png", data)
}
