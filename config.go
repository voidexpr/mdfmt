package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

func parseConfigFlags(args []string, output io.Writer) error {
	flags := newFlagSet("mdfmt config", output,
		"Usage: mdfmt config",
		"Show remembered roots, ports, and listening process IDs.")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return errors.New("config does not accept arguments")
	}
	return nil
}

func runConfig(stdout io.Writer) error {
	filename, err := portsFilePath()
	if err != nil {
		return err
	}
	registry, err := readPortsFile(filename)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	return writeConfigTable(stdout, registry, home, listeningPortPIDs())
}

func writeConfigTable(
	output io.Writer,
	registry portsFile,
	home string,
	listeners map[int][]int,
) error {
	roots := make([]string, 0, len(registry.Roots))
	for root := range registry.Roots {
		roots = append(roots, root)
	}
	sort.Slice(roots, func(i, j int) bool {
		left := registry.Roots[roots[i]].Port
		right := registry.Roots[roots[j]].Port
		if left != right {
			return left < right
		}
		return roots[i] < roots[j]
	})

	table := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "Pid\tPort\tRoot")
	for _, root := range roots {
		entry := registry.Roots[root]
		fmt.Fprintf(
			table,
			"%s\t%d\t%s\n",
			formatPIDs(listeners[entry.Port]),
			entry.Port,
			shortRoot(root, home),
		)
	}
	return table.Flush()
}

func formatPIDs(pids []int) string {
	if len(pids) == 0 {
		return "-"
	}
	pids = append([]int(nil), pids...)
	sort.Ints(pids)
	values := make([]string, 0, len(pids))
	previous := -1
	for _, pid := range pids {
		if pid == previous {
			continue
		}
		values = append(values, strconv.Itoa(pid))
		previous = pid
	}
	return strings.Join(values, ",")
}

func shortRoot(root, home string) string {
	if home == "" {
		return root
	}
	relative, err := filepath.Rel(home, root)
	if err != nil || filepath.IsAbs(relative) ||
		relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return root
	}
	if relative == "." {
		return "~"
	}
	return filepath.Join("~", relative)
}

func listeningPortPIDs() map[int][]int {
	executable := ""
	for _, candidate := range []string{"lsof", "/usr/sbin/lsof"} {
		resolved, err := exec.LookPath(candidate)
		if err == nil {
			executable = resolved
			break
		}
	}
	if executable == "" {
		return make(map[int][]int)
	}
	output, err := exec.Command(
		executable,
		"-nP",
		"-iTCP",
		"-sTCP:LISTEN",
		"-Fpn",
	).Output()
	if err != nil && len(output) == 0 {
		return make(map[int][]int)
	}
	return parseLsofListeners(output)
}

func parseLsofListeners(output []byte) map[int][]int {
	listeners := make(map[int][]int)
	pid := 0
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		field := scanner.Text()
		if len(field) < 2 {
			continue
		}
		switch field[0] {
		case 'p':
			pid, _ = strconv.Atoi(field[1:])
		case 'n':
			if pid == 0 {
				continue
			}
			colon := strings.LastIndexByte(field, ':')
			if colon < 0 {
				continue
			}
			port, err := strconv.Atoi(field[colon+1:])
			if err == nil {
				listeners[port] = append(listeners[port], pid)
			}
		}
	}
	return listeners
}
