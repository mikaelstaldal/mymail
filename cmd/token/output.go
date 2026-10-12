package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// tokenOutput reserves private output before issuing a token.
type tokenOutput struct {
	path     string
	file     *os.File
	stdout   io.Writer
	complete bool
}

func newTokenOutput(path string, stdout io.Writer) (*tokenOutput, error) {
	output := &tokenOutput{path: path, stdout: stdout}
	if path != "" {
		var err error
		output.file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
	}
	return output, nil
}

func (o *tokenOutput) cleanup() {
	if o.file != nil {
		_ = o.file.Close()
		if !o.complete {
			_ = os.Remove(o.path)
		}
	}
}

func (o *tokenOutput) write(token string) error {
	if o.file == nil {
		_, err := fmt.Fprintln(o.stdout, token)
		return err
	}
	_, writeErr := fmt.Fprintln(o.file, token)
	closeErr := o.file.Close()
	o.complete = writeErr == nil && closeErr == nil
	return errors.Join(writeErr, closeErr)
}
