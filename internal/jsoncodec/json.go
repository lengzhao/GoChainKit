// Package jsoncodec provides internal decoding mechanics, not protocol policy.
// Callers retain their own size limits, signatures and semantic validation.
package jsoncodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func Strict(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func Canonical(data []byte, out any) error {
	if err := Strict(data, out); err != nil {
		return err
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, encoded) {
		return fmt.Errorf("noncanonical JSON encoding")
	}
	return nil
}
