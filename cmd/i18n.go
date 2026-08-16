package cmd

import (
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// localizeCommandTree applies the active locale to every human-facing cobra
// string on the tree (Use/Short/Long/Example and each flag's Usage). Strings
// without a catalog entry are left unchanged, so an untranslated help text can
// never break the CLI. When the locale is zh, the root help/usage templates
// are swapped for Chinese variants; subcommands inherit the root templates
// through cobra's HelpTemplate/UsageTemplate lookup.
func localizeCommandTree(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.Use = i18n.Localize(c.Use)
		c.Short = i18n.Localize(c.Short)
		c.Long = i18n.Localize(c.Long)
		c.Example = i18n.Localize(c.Example)
		c.Flags().VisitAll(func(f *pflag.Flag) {
			f.Usage = i18n.Localize(f.Usage)
		})
		// cobra's auto-added -h flag usage is per-command ("help for <name>"),
		// which cannot be catalog-keyed. Rebuild it deterministically per
		// locale so localization stays reversible.
		if f := c.Flags().Lookup("help"); f != nil {
			if i18n.CurrentLocale() == i18n.ZH {
				f.Usage = "显示本命令帮助"
			} else {
				f.Usage = "help for " + c.Name()
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	if i18n.CurrentLocale() == i18n.ZH {
		root.SetUsageTemplate(zhUsageTemplate)
		root.SetHelpTemplate(zhHelpTemplate)
	} else {
		// Restore cobra's built-in templates after a previous zh pass.
		root.SetUsageTemplate("")
		root.SetHelpTemplate("")
	}
}

// zhUsageTemplate and zhHelpTemplate mirror cobra's default templates with
// translated section labels. The {{...}} fields and functions are cobra's own.
const zhUsageTemplate = `用法:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [命令]{{end}}{{if gt (len .Aliases) 0}}

别名:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

示例:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

可用命令:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $groups := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $groups.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

其他命令:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

标志:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

全局标志:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

其他帮助主题:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

使用 "{{.CommandPath}} [命令] --help" 查看某命令的更多信息。{{end}}
`

const zhHelpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`
