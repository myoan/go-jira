package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"

	"github.com/coryb/figtree"
	"github.com/coryb/kingpeon"
	"github.com/coryb/oreo"

	jira "gopkg.in/Netflix-Skunkworks/go-jira.v1"
	"gopkg.in/Netflix-Skunkworks/go-jira.v1/jiracli"
	"gopkg.in/Netflix-Skunkworks/go-jira.v1/jiracmd"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
	"gopkg.in/op/go-logging.v1"
)

var (
	log           = logging.MustGetLogger("jira")
	defaultFormat = func() string {
		format := os.Getenv("JIRA_LOG_FORMAT")
		if format != "" {
			return format
		}
		return "%{color}%{level:-5s}%{color:reset} %{message}"
	}()
)

func handleExit() {
	if e := recover(); e != nil {
		if exit, ok := e.(jiracli.Exit); ok {
			os.Exit(exit.Code)
		} else {
			fmt.Fprintf(os.Stderr, "%s\n%s", e, debug.Stack())
			os.Exit(1)
		}
	}
}

func increaseLogLevel(verbosity int) {
	logging.SetLevel(logging.GetLevel("")+logging.Level(verbosity), "")
	if logging.GetLevel("") > logging.DEBUG {
		oreo.TraceRequestBody = true
		oreo.TraceResponseBody = true
	}
}

var usage = `{{define "FormatCommand"}}\
{{if .FlagSummary}} {{.FlagSummary}}{{end}}\
{{range .Args}} {{if not .Required}}[{{end}}<{{.Name}}>{{if .Value|IsCumulative}}...{{end}}{{if not .Required}}]{{end}}{{end}}\
{{end}}\

{{define "FormatBriefCommands"}}\
{{range .FlattenedCommands}}\
{{if not .Hidden}}\
  {{ print .FullCommand ":" | printf "%-20s"}} {{.Help}}
{{end}}\
{{end}}\
{{end}}\

{{define "FormatCommands"}}\
{{range .FlattenedCommands}}\
{{if not .Hidden}}\
  {{.FullCommand}}{{if .Default}}*{{end}}{{template "FormatCommand" .}}
{{.Help|Wrap 4}}
{{with .Flags|FlagsToTwoColumns}}{{FormatTwoColumnsWithIndent . 4 2}}{{end}}
{{end}}\
{{end}}\
{{end}}\

{{define "FormatUsage"}}\
{{template "FormatCommand" .}}{{if .Commands}} <command> [<args> ...]{{end}}
{{if .Help}}
{{.Help|Wrap 0}}\
{{end}}\

{{end}}\

{{if .Context.SelectedCommand}}\
usage: {{.App.Name}} {{.Context.SelectedCommand}}{{template "FormatCommand" .Context.SelectedCommand}}
{{if .Context.SelectedCommand.Aliases }}\
{{range $top := .App.Commands}}\
{{if eq $top.FullCommand $.Context.SelectedCommand.FullCommand}}\
{{range $alias := $.Context.SelectedCommand.Aliases}}\
alias: {{$.App.Name}} {{$alias}}{{template "FormatCommand" $.Context.SelectedCommand}}
{{end}}\
{{else}}\
{{range $sub := $top.Commands}}\
{{if eq $sub.FullCommand $.Context.SelectedCommand.FullCommand}}\
{{range $alias := $.Context.SelectedCommand.Aliases}}\
alias: {{$.App.Name}} {{$top.Name}} {{$alias}}{{template "FormatCommand" $.Context.SelectedCommand}}
{{end}}\
{{end}}\
{{end}}\
{{end}}\
{{end}}\
{{end}}
{{if .Context.SelectedCommand.Help}}\
{{.Context.SelectedCommand.Help|Wrap 0}}
{{end}}\
{{else}}\
usage: {{.App.Name}}{{template "FormatUsage" .App}}
{{end}}\

{{if .App.Flags}}\
Global flags:
{{.App.Flags|FlagsToTwoColumns|FormatTwoColumns}}
{{end}}\
{{if .Context.SelectedCommand}}\
{{if and .Context.SelectedCommand.Flags|RequiredFlags}}\
Required flags:
{{.Context.SelectedCommand.Flags|RequiredFlags|FlagsToTwoColumns|FormatTwoColumns}}
{{end}}\
{{if .Context.SelectedCommand.Flags|OptionalFlags}}\
Optional flags:
{{.Context.SelectedCommand.Flags|OptionalFlags|FlagsToTwoColumns|FormatTwoColumns}}
{{end}}\
{{end}}\
{{if .Context.Args}}\
Args:
{{.Context.Args|ArgsToTwoColumns|FormatTwoColumns}}
{{end}}\
{{if .Context.SelectedCommand}}\
{{if .Context.SelectedCommand.Commands}}\
Subcommands:
{{template "FormatCommands" .Context.SelectedCommand}}
{{end}}\
{{else if .App.Commands}}\
Commands:
{{template "FormatBriefCommands" .App}}
{{end}}\
`

func main() {
	defer handleExit()
	logBackend := logging.NewLogBackend(os.Stderr, "", 0)
	format := os.Getenv("JIRA_LOG_FORMAT")
	if format == "" {
		format = defaultFormat
	}
	logging.SetBackend(
		logging.NewBackendFormatter(
			logBackend,
			logging.MustStringFormatter(format),
		),
	)
	if os.Getenv("JIRA_DEBUG") == "" {
		logging.SetLevel(logging.NOTICE, "")
	} else {
		logging.SetLevel(logging.DEBUG, "")
	}

	app := kingpin.New("jira", "Jira Command Line Interface")
	app.Command("version", "Prints version").PreAction(func(*kingpin.ParseContext) error {
		fmt.Println(jira.VERSION)
		panic(jiracli.Exit{Code: 0})
	})
	app.UsageTemplate(usage)

	var verbosity int
	app.Flag("verbose", "Increase verbosity for debugging").Short('v').PreAction(func(_ *kingpin.ParseContext) error {
		os.Setenv("JIRA_DEBUG", fmt.Sprintf("%d", verbosity))
		increaseLogLevel(1)
		return nil
	}).CounterVar(&verbosity)

	if os.Getenv("JIRA_DEBUG") != "" {
		if verbosity, err := strconv.Atoi(os.Getenv("JIRA_DEBUG")); err == nil {
			increaseLogLevel(verbosity)
		}
	}

	fig := figtree.NewFigTree()
	fig.EnvPrefix = "JIRA"
	fig.ConfigDir = ".jira.d"

	if err := os.MkdirAll(filepath.Join(jiracli.Homedir(), fig.ConfigDir), 0755); err != nil {
		log.Errorf("%s", err)
		panic(jiracli.Exit{Code: 1})
	}

	o := oreo.New().WithCookieFile(filepath.Join(jiracli.Homedir(), fig.ConfigDir, "cookies.js"))

	registry := []jiracli.CommandRegistry{
		{Command: "acknowledge", Entry: jiracmd.CmdTransitionRegistry("acknowledge"), Aliases: []string{"ack"}},
		{Command: "assign", Entry: jiracmd.CmdAssignRegistry(), Aliases: []string{"give"}},
		{Command: "attach create", Entry: jiracmd.CmdAttachCreateRegistry()},
		{Command: "attach get", Entry: jiracmd.CmdAttachGetRegistry()},
		{Command: "attach list", Entry: jiracmd.CmdAttachListRegistry(), Aliases: []string{"ls"}},
		{Command: "attach remove", Entry: jiracmd.CmdAttachRemoveRegistry(), Aliases: []string{"rm"}},
		{Command: "backlog", Entry: jiracmd.CmdTransitionRegistry("Backlog")},
		{Command: "block", Entry: jiracmd.CmdBlockRegistry()},
		{Command: "browse", Entry: jiracmd.CmdBrowseRegistry(), Aliases: []string{"b"}},
		{Command: "close", Entry: jiracmd.CmdTransitionRegistry("close")},
		{Command: "comment", Entry: jiracmd.CmdCommentRegistry()},
		{Command: "component add", Entry: jiracmd.CmdComponentAddRegistry()},
		{Command: "components", Entry: jiracmd.CmdComponentsRegistry()},
		{Command: "create", Entry: jiracmd.CmdCreateRegistry()},
		{Command: "createmeta", Entry: jiracmd.CmdCreateMetaRegistry()},
		{Command: "done", Entry: jiracmd.CmdTransitionRegistry("Done")},
		{Command: "dup", Entry: jiracmd.CmdDupRegistry()},
		{Command: "edit", Entry: jiracmd.CmdEditRegistry()},
		{Command: "editmeta", Entry: jiracmd.CmdEditMetaRegistry()},
		{Command: "epic add", Entry: jiracmd.CmdEpicAddRegistry()},
		{Command: "epic create", Entry: jiracmd.CmdEpicCreateRegistry()},
		{Command: "epic list", Entry: jiracmd.CmdEpicListRegistry(), Aliases: []string{"ls"}},
		{Command: "epic remove", Entry: jiracmd.CmdEpicRemoveRegistry(), Aliases: []string{"rm"}},
		{Command: "export-templates", Entry: jiracmd.CmdExportTemplatesRegistry()},
		{Command: "fields", Entry: jiracmd.CmdFieldsRegistry()},
		{Command: "in-progress", Entry: jiracmd.CmdTransitionRegistry("Progress"), Aliases: []string{"prog", "progress"}},
		{Command: "issuelink", Entry: jiracmd.CmdIssueLinkRegistry()},
		{Command: "issuelinktypes", Entry: jiracmd.CmdIssueLinkTypesRegistry()},
		{Command: "issuetypes", Entry: jiracmd.CmdIssueTypesRegistry()},
		{Command: "labels add", Entry: jiracmd.CmdLabelsAddRegistry()},
		{Command: "labels remove", Entry: jiracmd.CmdLabelsRemoveRegistry(), Aliases: []string{"rm"}},
		{Command: "labels set", Entry: jiracmd.CmdLabelsSetRegistry()},
		{Command: "list", Entry: jiracmd.CmdListRegistry(), Aliases: []string{"ls"}},
		{Command: "login", Entry: jiracmd.CmdLoginRegistry()},
		{Command: "logout", Entry: jiracmd.CmdLogoutRegistry()},
		{Command: "rank", Entry: jiracmd.CmdRankRegistry()},
		{Command: "reopen", Entry: jiracmd.CmdTransitionRegistry("reopen")},
		{Command: "request", Entry: jiracmd.CmdRequestRegistry(), Aliases: []string{"req"}},
		{Command: "resolve", Entry: jiracmd.CmdTransitionRegistry("resolve")},
		{Command: "start", Entry: jiracmd.CmdTransitionRegistry("start")},
		{Command: "stop", Entry: jiracmd.CmdTransitionRegistry("stop")},
		{Command: "subtask", Entry: jiracmd.CmdSubtaskRegistry()},
		{Command: "take", Entry: jiracmd.CmdTakeRegistry()},
		{Command: "todo", Entry: jiracmd.CmdTransitionRegistry("To Do")},
		{Command: "transition", Entry: jiracmd.CmdTransitionRegistry(""), Aliases: []string{"trans"}},
		{Command: "transitions", Entry: jiracmd.CmdTransitionsRegistry("transitions")},
		{Command: "transmeta", Entry: jiracmd.CmdTransitionsRegistry("debug")},
		{Command: "unassign", Entry: jiracmd.CmdUnassignRegistry()},
		{Command: "unexport-templates", Entry: jiracmd.CmdUnexportTemplatesRegistry()},
		{Command: "view", Entry: jiracmd.CmdViewRegistry()},
		{Command: "vote", Entry: jiracmd.CmdVoteRegistry()},
		{Command: "watch", Entry: jiracmd.CmdWatchRegistry()},
		{Command: "worklog add", Entry: jiracmd.CmdWorklogAddRegistry()},
		{Command: "worklog list", Entry: jiracmd.CmdWorklogListRegistry(), Default: true},
	}

	jiracli.Register(app, o, fig, registry)

	// register custom commands
	data := struct {
		CustomCommands kingpeon.DynamicCommands `yaml:"custom-commands" json:"custom-commands"`
	}{}

	if err := fig.LoadAllConfigs("config.yml", &data); err != nil {
		log.Errorf("%s", err)
		panic(jiracli.Exit{Code: 1})
	}

	if len(data.CustomCommands) > 0 {
		runner := syscall.Exec
		if runtime.GOOS == "windows" {
			runner = func(binary string, cmd []string, env []string) error {
				command := exec.Command(binary, cmd[1:]...)
				command.Stdin = os.Stdin
				command.Stdout = os.Stdout
				command.Stderr = os.Stderr
				command.Env = env
				return command.Run()
			}
		}

		tmp := map[string]interface{}{}
		fig.LoadAllConfigs("config.yml", &tmp)
		kingpeon.RegisterDynamicCommandsWithRunner(runner, app, data.CustomCommands, jiracli.TemplateProcessor())
	}

	app.Terminate(func(status int) {
		for _, arg := range os.Args {
			if arg == "-h" || arg == "--help" || len(os.Args) == 1 {
				panic(jiracli.Exit{Code: 0})
			}
		}
		panic(jiracli.Exit{Code: 1})
	})

	// checking for default usage of `jira ISSUE-123` but need to allow
	// for global options first like: `jira --user mothra ISSUE-123`
	ctx, err := app.ParseContext(os.Args[1:])
	if err != nil && ctx == nil {
		// This is an internal kingpin usage error, duplicate options/commands
		log.Fatalf("error: %s, ctx: %v", err, ctx)
	}

	if ctx != nil {
		if ctx.SelectedCommand == nil {
			next := ctx.Next()
			if next != nil {
				if ok, err := regexp.MatchString("^[A-Z]+-[0-9]+$", next.Value); err != nil {
					log.Errorf("Invalid Regex: %s", err)
				} else if ok {
					// insert "view" at i=1 (2nd position)
					os.Args = append(os.Args[:1], append([]string{"view"}, os.Args[1:]...)...)
				}
			}
		}
	}

	if _, err := app.Parse(os.Args[1:]); err != nil {
		if _, ok := err.(*jiracli.Error); ok {
			log.Errorf("%s", err)
			panic(jiracli.Exit{Code: 1})
		} else {
			ctx, _ := app.ParseContext(os.Args[1:])
			if ctx != nil {
				app.UsageForContext(ctx)
			}
			log.Errorf("Invalid Usage: %s", err)
			panic(jiracli.Exit{Code: 1})
		}
	}
}
