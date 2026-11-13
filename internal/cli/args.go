package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type flags struct {
	*flag.FlagSet
	name, usage    string
	stdout, stderr io.Writer
}

func (a *App) newFlags(name, usage string) *flags {
	fs := flag.NewFlagSet("stage "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return &flags{FlagSet: fs, name: name, usage: usage, stdout: a.Stdout, stderr: a.Stderr}
}

func (f *flags) intFlag(name, usage string) **int {
	target := new(*int)
	f.Func(name, usage, func(value string) error {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid int value: %q", value)
		}
		*target = &parsed
		return nil
	})
	return target
}

func (f *flags) usageLine() string {
	return strings.TrimSpace("usage: stage " + f.name + " " + f.usage)
}

// parse lets flags appear before, between or after the positional arguments.
func (f *flags) parse(args []string, names ...string) ([]string, error) {
	positional, err := f.parseInterspersed(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(f.stdout, f.usageLine())
		f.SetOutput(f.stdout)
		f.PrintDefaults()
		return nil, err
	}
	if err == nil && len(positional) < len(names) {
		err = errors.New("the following arguments are required: " + strings.Join(names[len(positional):], ", "))
	}
	if err == nil && len(positional) > len(names) {
		err = errors.New("unrecognized arguments: " + strings.Join(positional[len(names):], " "))
	}
	if err != nil {
		fmt.Fprintln(f.stderr, f.usageLine())
		return nil, &usageError{err.Error()}
	}
	return positional, nil
}

func (f *flags) parseOptional(args []string, max int) ([]string, error) {
	positional, err := f.parseInterspersed(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(f.stdout, f.usageLine())
		f.SetOutput(f.stdout)
		f.PrintDefaults()
		return nil, err
	}
	if err == nil && len(positional) > max {
		err = errors.New("unrecognized arguments: " + strings.Join(positional[max:], " "))
	}
	if err != nil {
		fmt.Fprintln(f.stderr, f.usageLine())
		return nil, &usageError{err.Error()}
	}
	return positional, nil
}

func (f *flags) parseInterspersed(args []string) ([]string, error) {
	var positional []string
	for {
		if err := f.Parse(args); err != nil {
			return nil, err
		}
		consumed := len(args) - f.NArg()
		rest := f.Args()
		if consumed > 0 && args[consumed-1] == "--" {
			return append(positional, rest...), nil
		}
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func isHelp(err error) bool { return errors.Is(err, flag.ErrHelp) }
