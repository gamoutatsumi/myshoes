package gh

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/whywaita/myshoes/internal/config"

	"github.com/whywaita/myshoes/pkg/logger"

	"github.com/google/go-github/v35/github"
)

// function pointers (for testing)
var (
	GHlistInstallations     = listInstallations
	GHlistAppsInstalledRepo = listAppsInstalledRepo
	GHNewClientGitHubApps   = NewClientGitHubApps
)

// GenerateGitHubAppsToken generate token of GitHub Apps using private key
// clientApps needs to response of `NewClientGitHubApps()`
func GenerateGitHubAppsToken(ctx context.Context, clientApps *github.Client, installationID int64, scope string) (string, *time.Time, error) {
	token, resp, err := clientApps.Apps.CreateInstallationToken(ctx, installationID, nil)
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate token from API: %w", err)
	}
	storeRateLimit(scope, resp.Rate)
	return *token.Token, token.ExpiresAt, nil
}

// GenerateRunnerRegistrationToken generate token for register runner
func GenerateRunnerRegistrationToken(ctx context.Context, gheDomain string, installationID int64, scope string) (string, *time.Time, error) {
	client, err := NewClientInstallation(gheDomain, installationID, config.Config.GitHub.AppID, config.Config.GitHub.PEMByte)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create NewClientInstallation: %w", err)
	}

	switch DetectScope(scope) {
	case Organization:
		token, _, err := client.Actions.CreateOrganizationRegistrationToken(ctx, scope)
		if err != nil {
			return "", nil, fmt.Errorf("failed to generate registration token for organization (scope: %s): %w", scope, err)
		}
		return *token.Token, &token.ExpiresAt.Time, nil
	case Repository:
		owner, repo := DivideScope(scope)
		token, _, err := client.Actions.CreateRegistrationToken(ctx, owner, repo)
		if err != nil {
			return "", nil, fmt.Errorf("failed to generate registration token for repository (scope: %s): %w", scope, err)
		}
		return *token.Token, &token.ExpiresAt.Time, nil
	default:
		return "", nil, fmt.Errorf("failed to detect scope (scope: %s)", scope)
	}
}

// IsInstalledGitHubApp check installed GitHub Apps in gheDomain + inputScope
func IsInstalledGitHubApp(ctx context.Context, gheDomain, inputScope string) (int64, error) {
	clientApps, err := GHNewClientGitHubApps(gheDomain, config.Config.GitHub.AppID, config.Config.GitHub.PEMByte)
	if err != nil {
		return -1, fmt.Errorf("failed to create client from GitHub Apps: %w", err)
	}
	installations, err := GHlistInstallations(ctx, clientApps)
	if err != nil {
		return -1, fmt.Errorf("failed to get list of installations: %w", err)
	}

	for _, i := range installations {
		if i.SuspendedAt != nil {
			continue
		}

		if strings.HasPrefix(inputScope, *i.Account.Login) {
			// i.Account.Login is username or Organization name.
			// e.g.) `https://github.com/example/sample` -> `example/sample`
			// strings.HasPrefix search scope include i.Account.Login.

			switch {
			case strings.EqualFold(*i.RepositorySelection, "all"):
				// "all" can use GitHub Apps in all repositories that joined i.Account.Login.
				return *i.ID, nil
			case strings.EqualFold(*i.RepositorySelection, "selected"):
				// "selected" can use GitHub Apps in only some repositories that permitted.
				// So, need to check more using other endpoint.
				err := isInstalledGitHubAppSelected(ctx, gheDomain, inputScope, *i.ID)
				if err == nil {
					// found
					return *i.ID, nil
				}
			}
		}
	}

	return -1, fmt.Errorf("%s/%s is not installed configured GitHub Apps", gheDomain, inputScope)
}

func isInstalledGitHubAppSelected(ctx context.Context, gheDomain, inputScope string, installationID int64) error {
	lr, err := GHlistAppsInstalledRepo(ctx, gheDomain, installationID, inputScope)
	if err != nil {
		return fmt.Errorf("failed to get list of installed repositories: %w", err)
	}

	s := DetectScope(inputScope)
	switch {
	case *lr.TotalCount <= 0:
		return fmt.Errorf("installed repository is not found")
	case s == Organization:
		// Scope is Organization and installed repository is exist
		// So GitHub Apps installed
		return nil
	case s != Repository:
		return fmt.Errorf("scope is unknown: %s", s)
	}

	// s == Repository
	for _, repo := range lr.Repositories {
		if strings.EqualFold(*repo.FullName, inputScope) {
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func listAppsInstalledRepo(ctx context.Context, gheDomain string, installationID int64, inputScope string) (*github.ListRepositories, error) {
	clientApps, err := NewClientGitHubApps(gheDomain, config.Config.GitHub.AppID, config.Config.GitHub.PEMByte)
	if err != nil {
		return nil, fmt.Errorf("failed to create github.Client from installationID: %w", err)
	}
	token, _, err := GenerateGitHubAppsToken(ctx, clientApps, installationID, inputScope)
	if err != nil {
		return nil, fmt.Errorf("failed to generate GitHub Apps Token: %w", err)
	}
	client, err := NewClient(ctx, token, gheDomain)
	if err != nil {
		return nil, fmt.Errorf("failed to NewClient: %w", err)
	}

	lr, _, err := client.Apps.ListRepos(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get installed repositories: %w", err)
	}

	return lr, nil
}

func listInstallations(ctx context.Context, clientApps *github.Client) ([]*github.Installation, error) {
	var opts = &github.ListOptions{
		Page:    0,
		PerPage: 10,
	}

	var installations []*github.Installation
	for {
		logger.Logf(true, "get installations from GitHub, page: %d, now all installations: %d", opts.Page, len(installations))
		is, resp, err := clientApps.Apps.ListInstallations(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list installations: %w", err)
		}
		installations = append(installations, is...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return installations, nil
}
