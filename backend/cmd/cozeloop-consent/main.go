package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/infrastructure/config"
	"interview-memory-agent/backend/internal/infrastructure/cozeloop"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "cozeloop-consent:", err)
		os.Exit(1)
	}
}

func run(args []string, input *os.File, output *os.File) error {
	if len(args) == 0 {
		return errors.New("usage: cozeloop-consent <status|grant|revoke> [--scopes trace-content,evaluation-dataset-content]")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load application configuration: %w", err)
	}
	action := args[0]
	flags := flag.NewFlagSet("cozeloop-consent", flag.ContinueOnError)
	flags.SetOutput(output)
	scopesArg := flags.String("scopes", "", "comma-separated content authorization scopes")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return errors.New("unexpected positional arguments")
	}
	recordPath := cfg.CozeLoop.ConsentPath
	pendingDir := cozeloop.PrivatePendingDirectory(cfg.DataDir)

	switch action {
	case "status":
		return printStatus(recordPath, cfg, output)
	case "grant":
		scopes, err := parseScopes(*scopesArg)
		if err != nil {
			return err
		}
		return grant(recordPath, cfg, scopes, input, output)
	case "revoke":
		return revoke(recordPath, pendingDir, input, output)
	default:
		return errors.New("action must be status, grant, or revoke")
	}
}

func parseScopes(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("grant requires --scopes with at least one explicit scope")
	}
	var scopes []string
	for _, scope := range strings.Split(value, ",") {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return nil, errors.New("scope list contains an empty value")
		}
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func grant(recordPath string, cfg config.Config, scopes []string, input *os.File, output *os.File) error {
	if strings.TrimSpace(cfg.CozeLoop.WorkspaceID) == "" {
		return errors.New("COZELOOP_WORKSPACE_ID must be configured before granting consent")
	}
	if err := printConsentNotice(cfg.CozeLoop.WorkspaceID, cfg.CozeLoop.APIBaseURL, scopes, output); err != nil {
		return err
	}
	fmt.Fprint(output, "Type I AUTHORIZE to record this permission: ")
	confirmation, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("could not read consent confirmation")
	}
	if strings.TrimSpace(confirmation) != "I AUTHORIZE" {
		return errors.New("consent was not recorded")
	}
	if err := cozeloop.GrantContentConsent(recordPath, cfg.CozeLoop.WorkspaceID, cfg.CozeLoop.APIBaseURL, scopes, time.Now()); err != nil {
		return err
	}
	fmt.Fprintln(output, "Consent recorded locally. No API token was stored.")
	return nil
}

func printConsentNotice(workspaceID, apiBaseURL string, scopes []string, output *os.File) error {
	if _, err := fmt.Fprintf(output, "\nCozeLoop content authorization notice (version %s)\n", cozeloop.ContentConsentNoticeVersion); err != nil {
		return err
	}
	fmt.Fprintf(output, "Recipient: %s (workspace %s)\n", apiBaseURL, workspaceID)
	fmt.Fprintf(output, "Purpose: evaluate and trace the interview review Agent.\n")
	fmt.Fprintln(output, "Selected content scopes:")
	for _, scope := range scopes {
		switch scope {
		case cozeloop.ConsentScopeTraceContent:
			fmt.Fprintln(output, "  - Trace content: model request/response and callback content may contain query, history, interview context, tool arguments/results.")
		case cozeloop.ConsentScopeEvaluationContent:
			fmt.Fprintln(output, "  - Evaluation dataset: case inputs, fixtures/references, actual outputs, sanitized tool trace, scores, usage, and trace IDs.")
		default:
			return errors.New("unknown content authorization scope")
		}
	}
	for _, scope := range scopes {
		if scope == cozeloop.ConsentScopeEvaluationContent {
			fmt.Fprintln(output, "Pending evaluation uploads are temporarily stored under APP_DATA_DIR/cozeloop/pending; Unix-like systems use 0600 files/0700 directories, while Windows relies on APP_DATA_DIR ACLs. Successful sync or revocation deletes them.")
			break
		}
	}
	fmt.Fprintln(output, "Uploaded data is subject to the configured CozeLoop workspace's retention and access policies. This local grant remains active until revoked.")
	return nil
}

func revoke(recordPath, pendingDir string, input *os.File, output *os.File) error {
	fmt.Fprintln(output, "Revocation stops future authorized content capture/upload and deletes pending local evaluation payloads.")
	fmt.Fprint(output, "Type REVOKE to confirm: ")
	confirmation, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.New("could not read revocation confirmation")
	}
	if strings.TrimSpace(confirmation) != "REVOKE" {
		return errors.New("revocation was not recorded")
	}
	if err := cozeloop.RevokeContentConsent(recordPath, pendingDir, time.Now()); err != nil {
		return err
	}
	fmt.Fprintln(output, "Consent revoked. Pending local content payloads were removed.")
	return nil
}

func printStatus(recordPath string, cfg config.Config, output *os.File) error {
	record, err := cozeloop.ReadContentConsent(recordPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(output, "No CozeLoop content authorization record exists.")
			return nil
		}
		return err
	}
	fmt.Fprintf(output, "Recipient: %s (workspace %s)\n", record.APIBaseURL, record.WorkspaceID)
	fmt.Fprintf(output, "Purpose: %s\n", record.Purpose)
	fmt.Fprintf(output, "Scopes: %s\n", strings.Join(record.Scopes, ", "))
	fmt.Fprintf(output, "Notice version: %s\nGranted at: %s\n", record.NoticeVersion, record.GrantedAt.UTC().Format(time.RFC3339))
	if record.RevokedAt != nil {
		fmt.Fprintf(output, "Revoked at: %s\n", record.RevokedAt.UTC().Format(time.RFC3339))
		return nil
	}
	if record.WorkspaceID != cfg.CozeLoop.WorkspaceID || strings.TrimRight(record.APIBaseURL, "/") != strings.TrimRight(cfg.CozeLoop.APIBaseURL, "/") {
		fmt.Fprintln(output, "Status: inactive (current workspace or API endpoint does not match)")
	} else if record.NoticeVersion != cozeloop.ContentConsentNoticeVersion {
		fmt.Fprintln(output, "Status: inactive (consent notice version changed)")
	} else {
		fmt.Fprintln(output, "Status: active until revoked")
	}
	return nil
}
