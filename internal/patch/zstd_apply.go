//go:build ignore

package patch

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/hashutil"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func applyZstdPayload(ctx context.Context, patch, output *os.File, offset, length uint64, expectedOutput fileState, callback progress.Callback, event progress.Event, decoders *decoderPool) (resultError error) {
	outputHash, err := hashutil.NewSizedAccumulator(expectedOutput.size)
	if err != nil {
		return err
	}
	defer func() {
		resultError = errors.Join(resultError, outputHash.Close())
	}()
	decoder, releaseDecoder, err := decoders.acquire(ctx, patch, offset, length)
	if err != nil {
		return err
	}
	defer releaseDecoder()
	err = decoder.DecompressToFile(ctx, output, expectedOutput.size, func(processed, total uint64) {
		eventCopy := event
		eventCopy.ProcessedBytes = processed
		eventCopy.TotalBytes = total
		progress.Report(callback, eventCopy)
	}, func(block []byte) error {
		_, writeError := outputHash.Write(block)
		return writeError
	})
	if err != nil {
		return err
	}
	actualOutputHash, err := outputHash.SumHex()
	if err != nil {
		return err
	}
	if actualOutputHash != expectedOutput.hash {
		return fmt.Errorf("generated output failed BLAKE3 tree verification")
	}
	return nil
}

func applyStandaloneReplaceConcurrent(ctx context.Context, source, patch, output *os.File, offset, length uint64, expectedInput, expectedOutput fileState, hashWorkers int, callback progress.Callback, event progress.Event, decoders *decoderPool) error {
	verifyContext, cancel := context.WithCancel(ctx)
	defer cancel()
	verificationDone := make(chan error, 1)
	verifyEvent := event
	go func() {
		verificationError := verifySourceForDecode(verifyContext, source, expectedInput, hashWorkers, callback, verifyEvent)
		if verificationError != nil {
			cancel()
		}
		verificationDone <- verificationError
	}()

	applyEvent := event
	applyEvent.Stage = progress.StageApplying
	applyEvent.ProcessedBytes = 0
	applyEvent.TotalBytes = expectedOutput.size
	progress.Report(callback, applyEvent)
	applyError := applyZstdPayload(verifyContext, patch, output, offset, length, expectedOutput, callback, applyEvent, decoders)
	if applyError != nil {
		cancel()
	}
	verificationError := <-verificationDone
	return errors.Join(applyError, verificationError)
}
