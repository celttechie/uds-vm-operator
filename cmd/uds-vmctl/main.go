package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/celttechie/uds-vm-operator/pkg/cli"
)

var (
	version = "v0.5.1"
	commit  = "none"
	date    = "unknown"

	kubeconfig string
	namespace  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "uds-vmctl",
		Short: "UDS Virtual Machine Operator CLI for Air-Gapped Mission Environments",
		Long: `uds-vmctl provides tactical edge and enterprise operations for UDSVirtualMachines,
including air-gap disk preparation, SHA-256 verification, Zarf packaging generation,
declarative power state management, and real-time inventory queries.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVarP(&kubeconfig, "kubeconfig", "k", os.Getenv("KUBECONFIG"), "Path to the kubeconfig file (defaults to $KUBECONFIG or ~/.kube/config)")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace (defaults to 'default' or all namespaces for inventory)")

	// Subcommands
	rootCmd.AddCommand(newPrepCmd())
	rootCmd.AddCommand(newPowerCmd())
	rootCmd.AddCommand(newInventoryCmd())
	rootCmd.AddCommand(newVersionCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newPrepCmd() *cobra.Command {
	var (
		vmName         string
		osType         string
		isGoldenImage  bool
		dataSourceName string
		dataSourceNs   string
		outputManifest string
		generateZarf   bool
		jsonOutput     bool
	)

	cmd := &cobra.Command{
		Use:   "prep <image-path>",
		Short: "Inspect disk image, verify air-gap standalone integrity, calculate PVC padding, and generate manifests",
		Long: `Inspects a RAW or QCOW2 disk image, verifies that it is standalone without external backing chains,
computes the cryptographic SHA-256 digest, calculates deterministic +6% filesystem padding, and generates
UDSVirtualMachine YAML and Zarf component definitions.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			imagePath := args[0]
			info, err := cli.InspectDiskImage(imagePath)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}

			fmt.Println("==================================================================")
			fmt.Println("        UDS AIR-GAP DISK IMAGE INSPECTION & PREPARATION          ")
			fmt.Println("==================================================================")
			fmt.Printf("File:                %s\n", info.Filename)
			fmt.Printf("Path:                %s\n", info.Path)
			fmt.Printf("Format:              %s\n", info.Format)
			fmt.Printf("Virtual Size:        %d bytes (%.2f GiB)\n", info.VirtualSizeBytes, float64(info.VirtualSizeBytes)/(1024*1024*1024))
			fmt.Printf("Actual Size:         %d bytes (%.2f GiB)\n", info.ActualSizeBytes, float64(info.ActualSizeBytes)/(1024*1024*1024))
			fmt.Printf("Required PVC Size:   %s (includes +6%% filesystem overhead)\n", info.CalculatedPVCSize)
			fmt.Printf("SHA-256 Digest:      %s\n", info.ChecksumSHA256)
			fmt.Printf("Air-Gap Verified:    PASSED (Standalone, 0 external backing references)\n")
			fmt.Println("==================================================================")

			manifestYAML := cli.GenerateUDSVirtualMachineYAMLWithDataSource(info, vmName, osType, isGoldenImage, dataSourceName, dataSourceNs)

			if isGoldenImage {
				dsYAML := cli.GenerateCDIDataSourceYAML(info, dataSourceName, dataSourceNs)
				fmt.Println("\n--- CDI DataSource & DataVolume Manifest ---")
				fmt.Print(dsYAML)
			}

			if outputManifest != "" {
				if err := os.WriteFile(outputManifest, []byte(manifestYAML), 0644); err != nil {
					return fmt.Errorf("failed to write manifest to %q: %w", outputManifest, err)
				}
				fmt.Printf("\nGenerated UDSVirtualMachine manifest written to %s\n", outputManifest)
			} else {
				fmt.Println("\n--- UDSVirtualMachine Manifest ---")
				fmt.Print(manifestYAML)
			}

			if generateZarf {
				compBase := vmName
				if compBase == "" {
					compBase = cli.DefaultResourceName(info.Filename)
				}
				fmt.Println("\n--- Zarf Component Definition ---")
				fmt.Print(cli.GenerateZarfComponentYAML(info, compBase+"-image", info.Path))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&vmName, "name", "", "Name of the UDSVirtualMachine (defaults to sanitized filename)")
	cmd.Flags().StringVar(&osType, "os", "linux", "Target operating system (linux or windows)")
	cmd.Flags().BoolVar(&isGoldenImage, "golden-image", false, "Generate manifest configured for CDI golden-image DataSource cloning")
	cmd.Flags().StringVar(&dataSourceName, "data-source-name", "", "Name of the target CDI DataSource for fast cloning")
	cmd.Flags().StringVar(&dataSourceNs, "data-source-ns", "", "Namespace of the target CDI DataSource (defaults to 'uds-golden-images')")
	cmd.Flags().StringVarP(&outputManifest, "output", "o", "", "File path to write generated UDSVirtualMachine YAML")
	cmd.Flags().BoolVar(&generateZarf, "zarf", false, "Generate Zarf component definition for air-gap package packaging")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output disk inspection metadata in JSON format")

	return cmd
}

func newPowerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "power <action> <vm-name>",
		Short: "Manage VM power state (start, stop, restart)",
		Long:  `Transitions UDSVirtualMachine runStrategy declaratively between 'Always' and 'Halted'.`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			action := args[0]
			vmName := args[1]

			ctx := cmd.Context()
			msg, err := cli.PowerAction(ctx, kubeconfig, namespace, vmName, action)
			if err != nil {
				return err
			}
			fmt.Println(msg)
			return nil
		},
	}

	return cmd
}

func newInventoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "List all managed UDSVirtualMachine workloads and their health",
		Long:  `Queries the cluster and presents a real-time tabular status of all UDSVirtualMachine resources.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return cli.ListInventory(ctx, kubeconfig, namespace)
		},
	}

	return cmd
}

func newVersionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print uds-vmctl version and build metadata",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("uds-vmctl version: %s\n", version)
			fmt.Printf("Git commit:        %s\n", commit)
			fmt.Printf("Build date:        %s\n", date)
		},
	}

	return cmd
}
