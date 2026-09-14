package desktopruntime

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	protocol "github.com/Serialeo/agentdock-protocol"
	"github.com/uvwt/agentdock/internal/desktopcontrol"
)

// RunBuiltinCommand uses the same local control socket as the native service panel.
// Offline requests fail explicitly; only the running core writes the authoritative choices.
func RunBuiltinCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || (args[0] != "list" && args[0] != "set") {
		return errors.New("用法: agentdock builtins <list|set> --runtime-root <目录> [--id browser|acp --enabled=true|false]")
	}
	flags := flag.NewFlagSet("agentdock builtins", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("runtime-root", "", "桌面运行目录")
	id := flags.String("id", "", "内置工具组")
	enabled := flags.Bool("enabled", false, "用户开关")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected builtins arguments")
	}
	var params any
	method := "builtins.list"
	if args[0] == "set" {
		present := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "enabled" {
				present = true
			}
		})
		if !present || *id == "" {
			return errors.New("id 和 enabled 必须显式指定")
		}
		method = "builtins.set"
		params = protocol.BuiltinUpdate{ID: *id, Enabled: enabled}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result json.RawMessage
	if err := desktopcontrol.Call(ctx, *root, method, params, &result); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(result)
}
