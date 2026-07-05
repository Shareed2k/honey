package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/safepath"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var docsCmd = &cobra.Command{
	Use:    "docs [dir]",
	Short:  "Generate markdown documentation for the CLI",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		dir := args[0]
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}

		// Disable auto-generated tag so the docs look cleaner
		rootCmd.DisableAutoGenTag = true

		// Docusaurus specifically requires frontmatter or unique headings
		err := doc.GenMarkdownTreeCustom(rootCmd, dir, func(s string) string {
			// Strip the `.md` from the filename for Docusaurus IDs
			name := filepath.Base(s)
			id := strings.TrimSuffix(name, ".md")
			// Return frontmatter required by Docusaurus
			return "---\nid: " + id + "\ntitle: " + strings.ReplaceAll(id, "_", " ") + "\n---\n\n"
		}, func(s string) string {
			return s
		})

		// Docusaurus MDX parser fails on raw { and } inside plain markdown text treating it like React expressions
		if err == nil {
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".md") {
					path := filepath.Clean(filepath.Join(dir, filepath.Base(e.Name())))
					b, _ := safepath.ReadFile(path)

					// Escape < and > to prevent Docusaurus from interpreting them as JSX tags
					content := strings.ReplaceAll(string(b), "<", "&lt;")
					content = strings.ReplaceAll(content, ">", "&gt;")

					// Wrap indented block quotes in proper markdown codeblocks so Docusaurus doesn't parse '{ }' as JSX
					content = strings.ReplaceAll(content, "	honey completion zsh &gt; \"${fpath[1]}/_honey\"", "```bash\nhoney completion zsh > \"${fpath[1]}/_honey\"\n```")
					content = strings.ReplaceAll(content, "	honey completion zsh &gt; $(brew --prefix)/share/zsh/site-functions/_honey", "```bash\nhoney completion zsh > $(brew --prefix)/share/zsh/site-functions/_honey\n```")

					// Escape bracket references in cue-validate synopsis
					content = strings.ReplaceAll(content, "script {local, remote}", "script \\{local, remote\\}")

					_ = os.WriteFile(path, []byte(content), 0o600)
				}
			}
		}

		return err
	},
}

func init() {
	rootCmd.AddCommand(docsCmd)
}
