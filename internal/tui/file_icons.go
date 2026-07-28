package tui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	fileIconDefault   = ""
	directoryIcon     = ""
	directoryOpenIcon = ""
)

type fileIconSpec struct {
	icon  string
	color lipgloss.Color
}

var (
	defaultFileIconColor = lipgloss.Color("#AAB2BF")
	directoryIconColor   = lipgloss.Color("#E5C07B")
)

// workspaceFileIcon returns a Nerd Font icon based on well-known filenames
// first, then on the file extension. Unknown files intentionally keep a
// generic file icon so every entry has the same visual alignment.
func workspaceFileIcon(name string) string {
	return workspaceFileIconSpec(name).icon
}

func renderWorkspaceFileIcon(name string) string {
	spec := workspaceFileIconSpec(name)
	return lipgloss.NewStyle().Foreground(spec.color).Render(spec.icon)
}

func renderDirectoryIcon(open bool) string {
	icon := directoryIcon
	if open {
		icon = directoryOpenIcon
	}
	return lipgloss.NewStyle().Foreground(directoryIconColor).Render(icon)
}

func workspaceFileIconSpec(name string) fileIconSpec {
	base := strings.ToLower(filepath.Base(name))
	switch base {
	case "dockerfile", "containerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml":
		return fileIconSpec{"", lipgloss.Color("#2496ED")}
	case "makefile", "gnumakefile", "cmakelists.txt":
		return fileIconSpec{"", lipgloss.Color("#6D8086")}
	case "license", "license.md", "license.txt", "copying":
		return fileIconSpec{"", lipgloss.Color("#E5C07B")}
	case ".gitignore", ".gitattributes", ".gitmodules":
		return fileIconSpec{"", lipgloss.Color("#F05032")}
	case "go.mod", "go.sum", "go.work":
		return fileIconSpec{"", lipgloss.Color("#00ADD8")}
	case "package.json", "package-lock.json", "tsconfig.json":
		return fileIconSpec{"", lipgloss.Color("#CBCB41")}
	}

	switch strings.ToLower(filepath.Ext(base)) {
	case ".go":
		return fileIconSpec{"", lipgloss.Color("#00ADD8")}
	case ".js", ".mjs", ".cjs":
		return fileIconSpec{"", lipgloss.Color("#F7DF1E")}
	case ".ts":
		return fileIconSpec{"", lipgloss.Color("#3178C6")}
	case ".jsx", ".tsx":
		return fileIconSpec{"", lipgloss.Color("#61DAFB")}
	case ".json", ".jsonc":
		return fileIconSpec{"", lipgloss.Color("#CBCB41")}
	case ".yaml", ".yml", ".toml", ".ini", ".conf", ".config", ".env":
		return fileIconSpec{"", lipgloss.Color("#6D8086")}
	case ".md", ".markdown", ".mdx":
		return fileIconSpec{"", lipgloss.Color("#519ABA")}
	case ".html", ".htm":
		return fileIconSpec{"", lipgloss.Color("#E34F26")}
	case ".css":
		return fileIconSpec{"", lipgloss.Color("#1572B6")}
	case ".scss", ".sass", ".less":
		return fileIconSpec{"", lipgloss.Color("#CD6799")}
	case ".py", ".pyi":
		return fileIconSpec{"", lipgloss.Color("#3776AB")}
	case ".rs":
		return fileIconSpec{"", lipgloss.Color("#DEA584")}
	case ".java", ".jar":
		return fileIconSpec{"", lipgloss.Color("#ED8B00")}
	case ".c", ".h":
		return fileIconSpec{"", lipgloss.Color("#A8B9CC")}
	case ".cc", ".cpp", ".cxx", ".hpp":
		return fileIconSpec{"", lipgloss.Color("#00599C")}
	case ".sh", ".bash", ".zsh", ".fish":
		return fileIconSpec{"", lipgloss.Color("#89E051")}
	case ".sql":
		return fileIconSpec{"", lipgloss.Color("#E38C00")}
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".bmp":
		return fileIconSpec{"", lipgloss.Color("#A074C4")}
	case ".zip", ".gz", ".tgz", ".bz2", ".xz", ".tar", ".7z", ".rar":
		return fileIconSpec{"", lipgloss.Color("#F2C94C")}
	case ".pdf":
		return fileIconSpec{"", lipgloss.Color("#F40F02")}
	case ".lock":
		return fileIconSpec{"", lipgloss.Color("#E5C07B")}
	}
	return fileIconSpec{fileIconDefault, defaultFileIconColor}
}
