package config

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	hallOwnerReason  = "the hall runs commands inside every stage, so only ~/.scoring-stage/config.toml may set it"
	defaultCAOPort   = 9889
	maxCAOPorts      = 8
	maxCommandArgs   = 32
	maxCommandLength = 1024
)

var (
	BuiltinProviders = []string{"kapellmeister"}
	hallKeys         = []string{"providers", "provider", "cao_ports", "keep"}
	keepPattern      = regexp.MustCompile(`^([0-9]{1,5})([dh])$`)
	providerName     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

type Hall struct {
	Providers []string
	Commands  map[string][]string
	CAOPorts  []int
	Keep      time.Duration
}

const (
	defaultKeep = 30 * 24 * time.Hour
	maxKeep     = 3650 * 24 * time.Hour
)

func decodeKeep(value any, where string) (time.Duration, error) {
	text, ok := value.(string)
	if !ok {
		return 0, errorf(`%s: keep must be a string such as "30d" or "12h"`, where)
	}
	if text == "0" {
		return 0, nil
	}
	match := keepPattern.FindStringSubmatch(text)
	if match == nil {
		return 0, errorf(`%s: keep must be whole days or hours, such as "30d" or "12h", or "0" to keep nothing`, where)
	}
	count, _ := strconv.Atoi(match[1])
	unit := time.Hour
	if match[2] == "d" {
		unit = 24 * time.Hour
	}
	if keep := time.Duration(count) * unit; keep <= maxKeep {
		return keep, nil
	}
	return 0, errorf("%s: keep can be at most 3650 days", where)
}

func refuseHall(data map[string]any, who string) error {
	if _, present := data["hall"]; present {
		return errorf("%s cannot set [hall]; %s", who, hallOwnerReason)
	}
	return nil
}

func decodeHall(value any, where string) (Hall, error) {
	hall := Hall{Commands: map[string][]string{}, CAOPorts: []int{defaultCAOPort}, Keep: defaultKeep}
	if value == nil {
		hall.Providers = slices.Clone(BuiltinProviders)
		return hall, nil
	}
	table, ok := value.(map[string]any)
	if !ok {
		return hall, errorf("%s: [hall] must be a table", where)
	}
	where += " [hall]"
	if err := checkKeys(table, hallKeys, where); err != nil {
		return hall, err
	}
	if err := decodeProviderCommands(table["provider"], where, hall.Commands); err != nil {
		return hall, err
	}
	if err := decodeProviderOrder(table, where, &hall); err != nil {
		return hall, err
	}
	if _, present := table["keep"]; present {
		keep, err := decodeKeep(table["keep"], where)
		if err != nil {
			return hall, err
		}
		hall.Keep = keep
	}
	if _, present := table["cao_ports"]; present {
		ports, err := decodeCAOPorts(table["cao_ports"], where)
		if err != nil {
			return hall, err
		}
		hall.CAOPorts = ports
	}
	return hall, nil
}

func decodeProviderCommands(value any, where string, commands map[string][]string) error {
	if value == nil {
		return nil
	}
	providers, ok := value.(map[string]any)
	if !ok {
		return errorf("%s: provider must be a table of providers", where)
	}
	for name, definition := range providers {
		at := where + " provider " + Printable(name)
		if !providerName.MatchString(name) {
			return errorf("%s: provider name must match %s", at, providerName)
		}
		if slices.Contains(BuiltinProviders, name) {
			return errorf("%s: %s is built in; leave it out of providers to turn it off", at, name)
		}
		table, ok := definition.(map[string]any)
		if !ok {
			return errorf("%s: must be a table with a command", at)
		}
		if err := checkKeys(table, []string{"command"}, at); err != nil {
			return err
		}
		command, err := decodeCommand(table["command"])
		if err != nil {
			return errorf("%s: command %v", at, err)
		}
		commands[name] = command
	}
	return nil
}

func decodeCommand(value any) ([]string, error) {
	list, ok := value.([]any)
	if !ok || len(list) == 0 || len(list) > maxCommandArgs {
		return nil, errorf("must be a list of 1 to %d strings, the program first", maxCommandArgs)
	}
	command := make([]string, 0, len(list))
	for _, item := range list {
		arg, ok := item.(string)
		if !ok || len(arg) > maxCommandLength || strings.ContainsRune(arg, 0) {
			return nil, errorf("must hold only strings of at most %d bytes with no NUL", maxCommandLength)
		}
		command = append(command, arg)
	}
	if command[0] == "" {
		return nil, errorf("must name a program first")
	}
	return command, nil
}

func decodeProviderOrder(table map[string]any, where string, hall *Hall) error {
	if _, present := table["providers"]; !present {
		defined := make([]string, 0, len(hall.Commands))
		for name := range hall.Commands {
			defined = append(defined, name)
		}
		sort.Strings(defined)
		hall.Providers = append(slices.Clone(BuiltinProviders), defined...)
		return nil
	}
	list, ok := table["providers"].([]any)
	if !ok {
		return errorf("%s: providers must be a list of provider names", where)
	}
	for _, item := range list {
		name, ok := item.(string)
		if !ok {
			return errorf("%s: providers must be a list of provider names", where)
		}
		if _, defined := hall.Commands[name]; !defined && !slices.Contains(BuiltinProviders, name) {
			return errorf("%s: %s is not a provider: define it under [hall.provider.%s] or name a built-in one", where, Printable(name), Printable(name))
		}
		if slices.Contains(hall.Providers, name) {
			return errorf("%s: providers names %s more than once", where, name)
		}
		hall.Providers = append(hall.Providers, name)
	}
	return nil
}

func decodeCAOPorts(value any, where string) ([]int, error) {
	list, ok := value.([]any)
	if !ok || len(list) == 0 || len(list) > maxCAOPorts {
		return nil, errorf("%s: cao_ports must be a list of 1 to %d ports", where, maxCAOPorts)
	}
	var ports []int
	for _, item := range list {
		port, ok := item.(int64)
		if !ok || port < 1 || port > 65535 || slices.Contains(ports, int(port)) {
			return nil, errorf("%s: cao_ports must hold distinct ports from 1 to 65535", where)
		}
		ports = append(ports, int(port))
	}
	return ports, nil
}
