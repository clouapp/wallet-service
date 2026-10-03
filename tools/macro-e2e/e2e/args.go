package e2e

import (
	"errors"
	"fmt"
	"strings"
)

const (
	flagPrefix      = "--"
	flagValueSep    = "="
	endOfFlagsToken = "--"
)

// ErrUsage marks command-line mistakes (exit code 2, like argparse).
var ErrUsage = errors.New("usage")

// ParsedArgs are the positionals and flags of one subcommand.
type ParsedArgs struct {
	Positional []string
	Values     map[string]string
	Switches   map[string]bool
}

// ParseArgs accepts flags anywhere (argparse style): switches take no value, value flags
// take the next argument or `--flag=value`. `--` ends flag parsing.
func ParseArgs(args []string, switches, valueFlags []string, positionalCount int) (ParsedArgs, error) {
	parsed := ParsedArgs{Values: map[string]string{}, Switches: map[string]bool{}}
	isSwitch, isValueFlag := toSet(switches), toSet(valueFlags)
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == endOfFlagsToken {
			parsed.Positional = append(parsed.Positional, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(argument, flagPrefix) {
			parsed.Positional = append(parsed.Positional, argument)
			continue
		}
		name, inlineValue, hasInlineValue := strings.Cut(strings.TrimPrefix(argument, flagPrefix), flagValueSep)
		switch {
		case isSwitch[name]:
			if hasInlineValue {
				return ParsedArgs{}, fmt.Errorf("%w: --%s takes no value", ErrUsage, name)
			}
			parsed.Switches[name] = true
		case isValueFlag[name]:
			if !hasInlineValue {
				if index+1 >= len(args) {
					return ParsedArgs{}, fmt.Errorf("%w: --%s needs a value", ErrUsage, name)
				}
				index++
				inlineValue = args[index]
			}
			parsed.Values[name] = inlineValue
		default:
			return ParsedArgs{}, fmt.Errorf("%w: unknown option --%s", ErrUsage, name)
		}
	}
	if len(parsed.Positional) != positionalCount {
		return ParsedArgs{}, fmt.Errorf("%w: expected %d arguments, got %d", ErrUsage, positionalCount, len(parsed.Positional))
	}
	return parsed, nil
}

func toSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
