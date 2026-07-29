package patch

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/nativev4"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
)

type openedPatch struct {
	file     *os.File
	parsed   patchformat.Patch
	digest   string
	path     string
	identity os.FileInfo
	size     uint64
}
type PreparedPatch struct {
	mutex  sync.Mutex
	path   string
	opened *openedPatch
}

func Open(path string) (patchformat.Patch, error) {
	opened, err := openPatch(path, "", false)
	if err != nil {
		return patchformat.Patch{}, err
	}
	defer opened.Close()
	return opened.parsed, nil
}
func OpenWithDigest(path string) (patchformat.Patch, string, error) {
	opened, err := openPatch(path, "", true)
	if err != nil {
		return patchformat.Patch{}, "", err
	}
	defer opened.Close()
	return opened.parsed, opened.digest, nil
}
func Prepare(path string) (*PreparedPatch, error) {
	opened, err := openPatch(path, "", true)
	if err != nil {
		return nil, err
	}
	return &PreparedPatch{path: path, opened: opened}, nil
}
func (p *PreparedPatch) Path() string {
	if p == nil {
		return ""
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.path
}
func (p *PreparedPatch) Digest() (string, error) {
	if p == nil {
		return "", fmt.Errorf("prepared patch is unavailable")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.opened == nil {
		return "", fmt.Errorf("prepared patch is closed")
	}
	return p.opened.digest, nil
}
func (p *PreparedPatch) Parsed() (patchformat.Patch, error) {
	if p == nil {
		return patchformat.Patch{}, fmt.Errorf("prepared patch is unavailable")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.opened == nil {
		return patchformat.Patch{}, fmt.Errorf("prepared patch is closed")
	}
	return cloneParsed(p.opened.parsed), nil
}
func (p *PreparedPatch) acquire() (*openedPatch, func() error, error) {
	if p == nil {
		return nil, nil, fmt.Errorf("prepared patch is unavailable")
	}
	p.mutex.Lock()
	if p.opened == nil {
		p.mutex.Unlock()
		return nil, nil, fmt.Errorf("prepared patch is closed")
	}
	if err := p.opened.verifyStable(); err != nil {
		p.mutex.Unlock()
		return nil, nil, err
	}
	return p.opened, func() error { p.mutex.Unlock(); return nil }, nil
}
func (p *PreparedPatch) Close() error {
	if p == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.opened == nil {
		return nil
	}
	err := p.opened.Close()
	p.opened = nil
	return err
}
func cloneParsed(parsed patchformat.Patch) patchformat.Patch {
	result := parsed
	result.Header.Files = make([]patchformat.FileEntry, len(parsed.Header.Files))
	for i := range parsed.Header.Files {
		entry := parsed.Header.Files[i]
		entry.SourceChunks = append([]patchformat.Digest(nil), entry.SourceChunks...)
		entry.TargetChunks = append([]patchformat.Digest(nil), entry.TargetChunks...)
		entry.ForwardWindows = append([]patchformat.WindowDescriptor(nil), entry.ForwardWindows...)
		entry.ReverseWindows = append([]patchformat.WindowDescriptor(nil), entry.ReverseWindows...)
		result.Header.Files[i] = entry
	}
	return result
}
func openPatch(path, expectedDigest string, calculateDigest bool) (*openedPatch, error) {
	if version := nativev4.ZstdVersion(); version != patchformat.SupportedZstdVersion {
		return nil, fmt.Errorf("Viper-Patcher V4 requires libzstd %s, linked version is %s", patchformat.SupportedZstdVersion, version)
	}
	file, identity, err := openStableRegular(path)
	if err != nil {
		return nil, err
	}
	closeErr := func(cause error) (*openedPatch, error) { _ = file.Close(); return nil, cause }
	if identity.Size() < 0 {
		return closeErr(fmt.Errorf("patch has invalid size"))
	}
	size := uint64(identity.Size())
	parsed, err := patchformat.DecodeAt(file, size, func(index []byte, expected patchformat.Digest) error {
		actual, err := nativev4.HashBytes(index)
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("V4 index digest mismatch")
		}
		return nil
	})
	if err != nil {
		return closeErr(err)
	}
	for _, entry := range parsed.Header.Files {
		sourceRoot, rootErr := nativev4.TreeRoot(entry.SourceSize, patchformat.IdentityChunkSize, entry.SourceChunks)
		if rootErr != nil || sourceRoot != entry.SourceDigest {
			return closeErr(fmt.Errorf("file %q source digest table is inconsistent", entry.Path))
		}
		targetRoot, rootErr := nativev4.TreeRoot(entry.TargetSize, patchformat.IdentityChunkSize, entry.TargetChunks)
		if rootErr != nil || targetRoot != entry.TargetDigest {
			return closeErr(fmt.Errorf("file %q target digest table is inconsistent", entry.Path))
		}
	}
	digest := ""
	if calculateDigest {
		session, err := nativev4.NewSession(file, nil, nil)
		if err != nil {
			return closeErr(err)
		}
		value, err := session.HashFile(context.Background(), false, size)
		session.Close()
		if err != nil {
			return closeErr(err)
		}
		digest = value.Hex()
	}
	if expectedDigest != "" && digest != expectedDigest {
		return closeErr(fmt.Errorf("selected patch changed after inspection"))
	}
	opened := &openedPatch{file: file, parsed: parsed, digest: digest, path: path, identity: identity, size: size}
	if err = opened.verifyStable(); err != nil {
		return closeErr(err)
	}
	return opened, nil
}
func (o *openedPatch) verifyStable() error {
	if o == nil || o.file == nil {
		return fmt.Errorf("patch is unavailable")
	}
	return stableUnchanged(o.file, o.path, o.identity)
}
func (o *openedPatch) Close() error {
	if o == nil || o.file == nil {
		return nil
	}
	err := o.file.Close()
	o.file = nil
	return err
}
