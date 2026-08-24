package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/waxarsatia/denyra/internal/reconcile"
)

type commandOptions struct {
	lidarrURL          string
	lidarrAPIKeyFile   string
	slskdURL           string
	slskdAPIKeyFile    string
	sftpgoURL          string
	sftpgoAdminFile    string
	sftpgoUploadFile   string
	navidromeURL       string
	navidromeAdminFile string
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "denyra-reconcile: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	var options commandOptions
	flags := flag.NewFlagSet("denyra-reconcile", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&options.lidarrURL, "lidarr-url", "", "Lidarr base URL")
	flags.StringVar(&options.lidarrAPIKeyFile, "lidarr-api-key-file", "", "Lidarr API key file")
	flags.StringVar(&options.slskdURL, "slskd-url", "", "slskd base URL")
	flags.StringVar(&options.slskdAPIKeyFile, "slskd-api-key-file", "", "slskd API key file")
	flags.StringVar(&options.sftpgoURL, "sftpgo-url", "", "SFTPGo base URL")
	flags.StringVar(&options.sftpgoAdminFile, "sftpgo-admin-file", "", "SFTPGo admin password file")
	flags.StringVar(&options.sftpgoUploadFile, "sftpgo-upload-file", "", "SFTPGo upload password file")
	flags.StringVar(&options.navidromeURL, "navidrome-url", "", "Navidrome base URL")
	flags.StringVar(&options.navidromeAdminFile, "navidrome-admin-file", "", "Navidrome admin password file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	required := map[string]string{
		"lidarr-url": options.lidarrURL, "lidarr-api-key-file": options.lidarrAPIKeyFile,
		"slskd-url": options.slskdURL, "slskd-api-key-file": options.slskdAPIKeyFile,
		"sftpgo-url": options.sftpgoURL, "sftpgo-admin-file": options.sftpgoAdminFile,
		"sftpgo-upload-file": options.sftpgoUploadFile, "navidrome-url": options.navidromeURL,
		"navidrome-admin-file": options.navidromeAdminFile,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("--%s is required", name)
		}
	}

	lidarrAPIKey, err := readSecret(options.lidarrAPIKeyFile)
	if err != nil {
		return fmt.Errorf("read Lidarr API key: %w", err)
	}
	slskdAPIKey, err := readSecret(options.slskdAPIKeyFile)
	if err != nil {
		return fmt.Errorf("read slskd API key: %w", err)
	}
	sftpgoAdmin, err := readSecret(options.sftpgoAdminFile)
	if err != nil {
		return fmt.Errorf("read SFTPGo administrator credential: %w", err)
	}
	sftpgoUpload, err := readSecret(options.sftpgoUploadFile)
	if err != nil {
		return fmt.Errorf("read SFTPGo upload credential: %w", err)
	}
	navidromeAdmin, err := readSecret(options.navidromeAdminFile)
	if err != nil {
		return fmt.Errorf("read Navidrome administrator credential: %w", err)
	}

	services := reconcile.Services(reconcile.Options{
		LidarrURL: options.lidarrURL, LidarrAPIKey: lidarrAPIKey,
		SlskdURL: options.slskdURL, SlskdAPIKey: slskdAPIKey,
		SFTPGoURL: options.sftpgoURL, SFTPGoAdminPassword: sftpgoAdmin, SFTPGoUploadPassword: sftpgoUpload,
		NavidromeURL: options.navidromeURL, NavidromeAdminPassword: navidromeAdmin,
		HTTP: &http.Client{Timeout: 30 * time.Second},
	})
	outcomes, err := reconcile.Run(ctx, services)
	if err != nil {
		return err
	}
	for _, outcome := range outcomes {
		fmt.Printf("%s: %s\n", outcome.Service, outcome.Message)
	}
	return nil
}

func readSecret(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.Trim(string(content), "\r\n")
	if value == "" {
		return "", errors.New("secret is empty")
	}
	return value, nil
}
