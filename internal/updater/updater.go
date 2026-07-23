package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

const (
	latestReleaseURL = "https://api.github.com/repos/SKAIBlue/zzam-tiger/releases/latest"
	installScriptURL = "https://raw.githubusercontent.com/SKAIBlue/zzam-tiger/main/install.sh"
)

// CheckLatest reports the latest release tag and whether it is newer than the
// version embedded in the running binary. Development and malformed versions
// are ignored so local builds do not constantly advertise an update.
func CheckLatest(ctx context.Context, current string) (string, bool, error) {
	return checkLatest(ctx, http.DefaultClient, latestReleaseURL, current)
}

func checkLatest(ctx context.Context, client *http.Client, releaseURL, current string) (string, bool, error) {
	currentVersion, ok := parseVersion(current)
	if !ok {
		return "", false, nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", false, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := client.Do(request)
	if err != nil {
		return "", false, fmt.Errorf("check latest release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("check latest release: GitHub returned %s", response.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return "", false, fmt.Errorf("decode latest release: %w", err)
	}
	latestVersion, ok := parseVersion(release.TagName)
	if !ok {
		return "", false, fmt.Errorf("latest release has an invalid version tag %q", release.TagName)
	}
	return release.TagName, compareVersion(latestVersion, currentVersion) > 0, nil
}

// InstallCommand returns the documented installer invocation. Bubble Tea runs
// it outside the alternate screen so the user can see download and error output.
func InstallCommand() *exec.Cmd {
	return exec.Command("sh", "-c", "curl -fsSL "+installScriptURL+" | sh")
}

type version struct {
	major, minor, patch int
	prerelease          bool
}

func parseVersion(value string) (version, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	core := value
	prerelease := false
	if index := strings.IndexAny(core, "-+"); index >= 0 {
		prerelease = core[index] == '-'
		core = core[:index]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	numbers := make([]int, 3)
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return version{}, false
		}
		numbers[index] = number
	}
	return version{major: numbers[0], minor: numbers[1], patch: numbers[2], prerelease: prerelease}, true
}

func compareVersion(left, right version) int {
	for _, pair := range [][2]int{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if left.prerelease == right.prerelease {
		return 0
	}
	if left.prerelease {
		return -1
	}
	return 1
}
