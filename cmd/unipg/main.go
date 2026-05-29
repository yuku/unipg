package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yuku/unipg"
	"github.com/yuku/unipg/compilers/stringify"
	"github.com/yuku/unipg/parsers/text"
	"github.com/yuku/unipg/transformers/comment"
	"github.com/yuku/unipg/transformers/extractfk"
	"github.com/yuku/unipg/transformers/reorder"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	flag.Usage = func() {
		exe := filepath.Base(os.Args[0])
		fmt.Fprintf(os.Stderr, "Usage: %s <file.sql>\n", exe)
		fmt.Fprintf(os.Stderr, "       cat schema.sql | %s -\n", exe)
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	filename := flag.Arg(0)
	var content []byte
	var err error

	if filename == "-" {
		content, err = io.ReadAll(os.Stdin)
	} else {
		content, err = os.ReadFile(filename)
	}
	if err != nil {
		return err
	}

	processor := unipg.New(
		text.New(),
		[]unipg.Transformer{
			comment.New(),
			extractfk.New(),
			reorder.New(),
		},
		stringify.New(),
	)

	output, err := processor.Process(string(content))
	if err != nil {
		return fmt.Errorf("failed to process SQL: %w", err)
	}

	fmt.Println(output)
	return nil
}
