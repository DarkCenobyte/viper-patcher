package patch

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func applyCopyAddConcurrent(ctx context.Context, source, patchFile, output *os.File, offset, length, expandedLength uint64, expectedInput, expectedOutput fileState, hashWorkers int, callback progress.Callback, event progress.Event, decoders *decoderPool) error {
	workContext, cancel := context.WithCancel(ctx)
	defer cancel()

	verificationDone := make(chan error, 1)
	verifyEvent := event
	go func() {
		verificationError := verifySourceForDecode(workContext, source, expectedInput, hashWorkers, callback, verifyEvent)
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

	applyError := func() error {
		decoder, releaseDecoder, err := decoders.acquire(workContext, patchFile, offset, length)
		if err != nil {
			return err
		}
		defer releaseDecoder()
		return applyCompressedInstructionStream(workContext, decoder, expandedLength, func(reader io.Reader) error {
			return applyCopyAddStreamContext(workContext, source, reader, output, expectedInput.size, expectedOutput.size, expectedOutput.hash, callback, applyEvent)
		})
	}()
	if applyError != nil {
		cancel()
	}
	verificationError := <-verificationDone
	return errors.Join(applyError, verificationError)
}
