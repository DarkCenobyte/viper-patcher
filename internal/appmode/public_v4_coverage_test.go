//go:build !windows

package appmode

import "testing"

func TestV4PublicPrepareGUI(t *testing.T) {
	PrepareGUI()
}
