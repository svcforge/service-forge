package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/svcforge/service-forge/templates"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "svcforge",
		Short: "Service Forge microservice scaffolding tool",
	}
	cmd.AddCommand(newCmd(), addCmd(), protoCmd(), doctorCmd())
	return cmd
}

func newCmd() *cobra.Command {
	var opts templates.ProjectOptions
	cmd := &cobra.Command{
		Use:   "new <project>",
		Short: "Create a new Service Forge project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			return templates.GenerateProject(cwd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.DB, "db", "noop", "database adapter")
	cmd.Flags().StringVar(&opts.Cache, "cache", "memory", "cache adapter")
	cmd.Flags().StringVar(&opts.MQ, "mq", "memory", "message queue adapter")
	cmd.Flags().StringVar(&opts.Registry, "registry", "memory", "service registry adapter")
	cmd.Flags().StringVar(&opts.Tracing, "tracing", "noop", "tracing adapter")
	cmd.Flags().StringVar(&opts.Replace, "replace", "", "local replacement path for github.com/svcforge/service-forge")
	return cmd
}

func addCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add framework resources",
	}
	service := &cobra.Command{
		Use:   "service <name>",
		Short: "Add a gRPC-only service skeleton",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			return templates.GenerateService(cwd, args[0])
		},
	}
	cmd.AddCommand(service)
	return cmd
}

func protoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proto",
		Short: "Protocol buffer helpers",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "gen",
		Short: "Run buf generate when buf is installed",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := exec.LookPath("buf")
			if err != nil {
				return fmt.Errorf("buf is not installed: %w", err)
			}
			run := exec.Command("buf", "generate")
			run.Stdout = os.Stdout
			run.Stderr = os.Stderr
			return run.Run()
		},
	})
	return cmd
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check Service Forge project basics",
		RunE: func(cmd *cobra.Command, args []string) error {
			required := []string{"go.mod", "config", "api/proto"}
			for _, path := range required {
				if _, err := os.Stat(path); err != nil {
					return fmt.Errorf("missing %s", path)
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Service Forge project looks ok")
			return nil
		},
	}
}
