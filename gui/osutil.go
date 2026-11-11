package main

import (
	"errors"
	"io/fs"
	"os"
)

func readFile(p string) ([]byte, error) { return os.ReadFile(p) }
func isNotExist(err error) bool         { return errors.Is(err, fs.ErrNotExist) }
