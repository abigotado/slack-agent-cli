package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: atomicrename SOURCE_DIR TARGET_DIR")
	}
	source, target := args[0], args[1]
	if !filepath.IsAbs(source) || !filepath.IsAbs(target) {
		return errors.New("source and target must be absolute paths")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect source directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("source must be a real directory")
	}
	if err := renameNoReplace(source, target); err != nil {
		return fmt.Errorf("publish directory without replacement: %w", err)
	}
	return nil
}
