package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

func readFrame(reader io.Reader) ([]byte, error) {
	var size [4]byte
	if _, err := io.ReadFull(reader, size[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > 256*1024 {
		return nil, errors.New("invalid message length")
	}
	raw := make([]byte, n)
	_, err := io.ReadFull(reader, raw)
	return raw, err
}
func writeFrame(writer io.Writer, raw []byte) error {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(raw)))
	_, err := io.Copy(writer, io.MultiReader(bytes.NewReader(size[:]), bytes.NewReader(raw)))
	return err
}
