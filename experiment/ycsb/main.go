/*
 * SPDX-FileCopyrightText: © 2017-2025 Istari Digital, Inc.
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

type config struct {
	GoYCSBDir       string            `json:"goYCSBDir"`
	GoYCSBBinary    string            `json:"goYCSBBinary"`
	WorkloadFile    string            `json:"workloadFile"`
	DB              string            `json:"db"`
	BadgerDir       string            `json:"badgerDir"`
	BadgerValueDir  string            `json:"badgerValueDir"`
	GoCache         string            `json:"goCache"`
	GoModCache      string            `json:"goModCache"`
	ExtraProperties map[string]string `json:"extraProperties"`
	LoadProperties  map[string]string `json:"loadProperties"`
	RunProperties   map[string]string `json:"runProperties"`
}

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fatalf("usage: go run ./experiment/ycsb <config.json> [build|load|run|all]")
	}

	phase := "all"
	if len(os.Args) == 3 {
		phase = os.Args[2]
	}
	if phase != "build" && phase != "load" && phase != "run" && phase != "all" {
		fatalf("invalid phase %q, expected one of: build, load, run, all", phase)
	}

	cfg, err := loadConfig(os.Args[1])
	if err != nil {
		fatalf("load config: %v", err)
	}

	if err := run(cfg, phase); err != nil {
		fatalf("%v", err)
	}
}

func run(cfg config, phase string) error {
	env := append([]string{}, os.Environ()...)
	if cfg.GoCache != "" {
		env = append(env, "GOCACHE="+cfg.GoCache)
	}
	if cfg.GoModCache != "" {
		env = append(env, "GOMODCACHE="+cfg.GoModCache)
	}

	if phase == "build" || phase == "all" {
		fmt.Println("[ycsb] building go-ycsb binary")
		if err := runCmd(cfg.GoYCSBDir, env, "go", "build", "-o", cfg.GoYCSBBinary, "./cmd/go-ycsb"); err != nil {
			return err
		}
	}

	if phase == "load" || phase == "all" {
		fmt.Println("[ycsb] running load phase")
		if err := runYCSB(cfg, env, "load", cfg.LoadProperties); err != nil {
			return err
		}
	}

	if phase == "run" || phase == "all" {
		fmt.Println("[ycsb] running run phase")
		if err := runYCSB(cfg, env, "run", cfg.RunProperties); err != nil {
			return err
		}
	}

	return nil
}

func runYCSB(cfg config, env []string, mode string, phaseProps map[string]string) error {
	props := map[string]string{
		"badger.dir":      cfg.BadgerDir,
		"badger.valuedir": cfg.BadgerValueDir,
	}
	mergeProps(props, cfg.ExtraProperties)
	mergeProps(props, phaseProps)

	args := []string{mode, cfg.DB, "-P", cfg.WorkloadFile}
	keys := sortedKeys(props)
	for _, key := range keys {
		args = append(args, "-p", fmt.Sprintf("%s=%s", key, props[key]))
	}

	bin := filepath.Join(cfg.GoYCSBDir, cfg.GoYCSBBinary)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("go-ycsb binary not found at %s, run build phase first: %w", bin, err)
	}

	return runCmd("", env, bin, args...)
}

func runCmd(dir string, env []string, name string, args ...string) error {
	fmt.Printf("[exec] %s %v\n", name, args)
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if dir != "" {
		cmd.Dir = dir
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run command %s failed: %w", name, err)
	}
	return nil
}

func loadConfig(path string) (config, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return config{}, err
	}
	if err := cfg.normalizeAndValidate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (c *config) normalizeAndValidate() error {
	if c.GoYCSBDir == "" {
		return errors.New("missing required field: goYCSBDir")
	}
	if c.WorkloadFile == "" {
		return errors.New("missing required field: workloadFile")
	}
	if c.BadgerDir == "" {
		return errors.New("missing required field: badgerDir")
	}

	if c.GoYCSBBinary == "" {
		c.GoYCSBBinary = "go-ycsb"
	}
	if c.DB == "" {
		c.DB = "badger"
	}
	if c.BadgerValueDir == "" {
		c.BadgerValueDir = c.BadgerDir
	}
	if c.GoCache == "" {
		c.GoCache = "/tmp/badger-gocache"
	}
	if c.GoModCache == "" {
		c.GoModCache = "/tmp/badger-gomodcache"
	}

	if c.ExtraProperties == nil {
		c.ExtraProperties = map[string]string{}
	}
	if c.LoadProperties == nil {
		c.LoadProperties = map[string]string{}
	}
	if c.RunProperties == nil {
		c.RunProperties = map[string]string{}
	}

	if fi, err := os.Stat(c.GoYCSBDir); err != nil {
		return fmt.Errorf("goYCSBDir does not exist: %w", err)
	} else if !fi.IsDir() {
		return fmt.Errorf("goYCSBDir is not a directory: %s", c.GoYCSBDir)
	}

	workloadPath := filepath.Join(c.GoYCSBDir, c.WorkloadFile)
	if _, err := os.Stat(workloadPath); err != nil {
		return fmt.Errorf("workload file not found: %s: %w", workloadPath, err)
	}

	return nil
}

func mergeProps(dst map[string]string, src map[string]string) {
	for k, v := range src {
		dst[k] = v
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
