package main

import (
	"fmt"
	"os"

	"github.com/jessevdk/go-flags"

	"github.com/zricethezav/swg/pkg/matcher"
)

var opts struct {
	ReplaceInf  int    `short:"m" long:"replace-inf" description:"replaces inf (*/+) with a bounded max" default:"-1"`
	PatternFile string `short:"f" long:"pattern-file" description:"file containing patterns to search for"`
	TargetPath  string `short:"d" long:"target-path" description:"path to search for patterns" default:"."`
}

func main() {
	var (
		m    *matcher.Matcher
		args []string
		err  error
	)
	if args, err = flags.Parse(&opts); err != nil {
		switch flagsErr := err.(type) {
		case flags.ErrorType:
			if flagsErr == flags.ErrHelp {
				os.Exit(0)
			}
			os.Exit(1)
		default:
			os.Exit(1)
		}
	}

	if opts.PatternFile == "" {
		m, err = matcher.NewMatcher(args, opts.ReplaceInf)
	} else {
		// use pattern file, one pattern per line
		m, err = matcher.NewMatcher([]string{os.Args[1]}, opts.ReplaceInf)
	}

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if opts.TargetPath != "." {
		if _, err := os.Stat(opts.TargetPath); os.IsNotExist(err) {
			fmt.Println("target path does not exist")
			os.Exit(1)
		}
	}

	m.SearchDir(opts.TargetPath)

}
