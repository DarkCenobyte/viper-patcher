package patchformat

import (
	"bytes"
	"fmt"
)

const (
	ContainerFlagReverse          uint32 = 1 << 0
	ContainerFlagFineVerification uint32 = 1 << 1
	supportedContainerFlags              = ContainerFlagReverse | ContainerFlagFineVerification

	FineVerificationVersion = 1
	MaxFineDigestsPerFile   = 1 << 22
)

var fineVerificationMagic = [8]byte{'V', 'I', 'P', 'R', 'F', 'V', '1', 0}

// FineDigest authenticates one fixed-size source band. Digests are full
// BLAKE3-256 values; they are never truncated or used as composable BLAKE3
// chaining values. The existing 8 MiB identity table and file root remain
// unchanged.
type FineDigest struct {
	Index  uint64
	Digest Digest
}

func containerFlags(reverse, fine bool) uint32 {
	var flags uint32
	if reverse {
		flags |= ContainerFlagReverse
	}
	if fine {
		flags |= ContainerFlagFineVerification
	}
	return flags
}

func entryHasFineVerification(entry FileEntry) bool {
	return entry.SourceFineChunkSize != 0 || entry.TargetFineChunkSize != 0 ||
		len(entry.SourceFineChunks) != 0 || len(entry.TargetFineChunks) != 0
}

func hasFineVerification(files []FileEntry) bool {
	for _, entry := range files {
		if entryHasFineVerification(entry) {
			return true
		}
	}
	return false
}

func validFineChunkSize(size uint32) bool {
	switch size {
	case 64 << 10, 256 << 10, 1 << 20:
		return true
	default:
		return false
	}
}

func fineChunkCount(size uint64, chunkSize uint32) uint64 {
	if size == 0 || chunkSize == 0 {
		return 0
	}
	return 1 + (size-1)/uint64(chunkSize)
}

func validateFineDigestTable(fileSize uint64, chunkSize uint32, values []FineDigest) error {
	if len(values) == 0 {
		if chunkSize != 0 {
			return fmt.Errorf("empty table declares a chunk size")
		}
		return nil
	}
	if !validFineChunkSize(chunkSize) {
		return fmt.Errorf("invalid fine chunk size %d", chunkSize)
	}
	if len(values) > MaxFineDigestsPerFile {
		return fmt.Errorf("fine digest table contains too many entries")
	}
	count := fineChunkCount(fileSize, chunkSize)
	var previous uint64
	for index, value := range values {
		if value.Index >= count {
			return fmt.Errorf("fine digest index %d exceeds file", value.Index)
		}
		if index != 0 && value.Index <= previous {
			return fmt.Errorf("fine digest indexes are not strictly increasing")
		}
		previous = value.Index
	}
	return nil
}

func encodeFineVerification(buffer *bytes.Buffer, files []FileEntry) error {
	buffer.Write(fineVerificationMagic[:])
	writeU32(buffer, FineVerificationVersion)
	writeU32(buffer, uint32(len(files)))
	for index := range files {
		entry := &files[index]
		if err := validateFineDigestTable(entry.SourceSize, entry.SourceFineChunkSize, entry.SourceFineChunks); err != nil {
			return fmt.Errorf("encode file entry %d source fine digests: %w", index, err)
		}
		if err := validateFineDigestTable(entry.TargetSize, entry.TargetFineChunkSize, entry.TargetFineChunks); err != nil {
			return fmt.Errorf("encode file entry %d target fine digests: %w", index, err)
		}
		writeU32(buffer, entry.SourceFineChunkSize)
		writeU32(buffer, entry.TargetFineChunkSize)
		writeU32(buffer, uint32(len(entry.SourceFineChunks)))
		writeU32(buffer, uint32(len(entry.TargetFineChunks)))
		for _, value := range entry.SourceFineChunks {
			writeU64(buffer, value.Index)
			buffer.Write(value.Digest[:])
		}
		for _, value := range entry.TargetFineChunks {
			writeU64(buffer, value.Index)
			buffer.Write(value.Digest[:])
		}
	}
	return nil
}

func decodeFineVerification(reader *indexReader, files []FileEntry) error {
	magic, err := reader.bytes(len(fineVerificationMagic))
	if err != nil {
		return fmt.Errorf("decode fine verification magic: %w", err)
	}
	if !bytes.Equal(magic, fineVerificationMagic[:]) {
		return fmt.Errorf("invalid fine verification magic")
	}
	version, err := reader.u32()
	if err != nil {
		return err
	}
	if version != FineVerificationVersion {
		return fmt.Errorf("unsupported fine verification version %d", version)
	}
	fileCount, err := reader.u32()
	if err != nil {
		return err
	}
	if uint64(fileCount) != uint64(len(files)) {
		return fmt.Errorf("fine verification file count disagrees with index")
	}
	for index := range files {
		entry := &files[index]
		entry.SourceFineChunkSize, err = reader.u32()
		if err != nil {
			return err
		}
		entry.TargetFineChunkSize, err = reader.u32()
		if err != nil {
			return err
		}
		sourceCount, err := reader.u32()
		if err != nil {
			return err
		}
		targetCount, err := reader.u32()
		if err != nil {
			return err
		}
		if sourceCount > MaxFineDigestsPerFile || targetCount > MaxFineDigestsPerFile {
			return fmt.Errorf("fine verification table contains too many entries")
		}
		total := uint64(sourceCount) + uint64(targetCount)
		if total > ^uint64(0)/40 || total*40 > uint64(reader.remaining()) {
			return fmt.Errorf("fine verification tables exceed remaining index data")
		}
		entry.SourceFineChunks = make([]FineDigest, int(sourceCount))
		entry.TargetFineChunks = make([]FineDigest, int(targetCount))
		for valueIndex := range entry.SourceFineChunks {
			value, readErr := decodeFineDigest(reader)
			if readErr != nil {
				return readErr
			}
			entry.SourceFineChunks[valueIndex] = value
		}
		for valueIndex := range entry.TargetFineChunks {
			value, readErr := decodeFineDigest(reader)
			if readErr != nil {
				return readErr
			}
			entry.TargetFineChunks[valueIndex] = value
		}
		if err := validateFineDigestTable(entry.SourceSize, entry.SourceFineChunkSize, entry.SourceFineChunks); err != nil {
			return fmt.Errorf("file entry %d source fine digests: %w", index, err)
		}
		if err := validateFineDigestTable(entry.TargetSize, entry.TargetFineChunkSize, entry.TargetFineChunks); err != nil {
			return fmt.Errorf("file entry %d target fine digests: %w", index, err)
		}
	}
	return nil
}

func decodeFineDigest(reader *indexReader) (FineDigest, error) {
	var value FineDigest
	var err error
	value.Index, err = reader.u64()
	if err != nil {
		return value, err
	}
	digest, err := reader.bytes(len(value.Digest))
	if err != nil {
		return value, err
	}
	copy(value.Digest[:], digest)
	return value, nil
}
