// fstab Manager: a desktop app for managing /etc/fstab, backups and
// home-folder symlinks. It runs in its own window (Wails, using the system's
// WebKitGTK); there is no browser and no network listener. Companion to the
// Python CLI "fstabctl"; both read and write the same backup and bundle formats.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// runExec is the privileged helper: the window re-runs this binary through
// pkexec (or sudo) as "fstab-manager exec ... -- ARGS" to apply one change.
// Settings arrive as arguments because pkexec scrubs the environment.
func runExec(rest []string) int {
	apply := false
	var args []string
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; a {
		case "-y":
			apply = true
		case "-n":
			apply = false
		case "--real-user", "--backup-dir", "--fstab", "--home":
			if i+1 >= len(rest) {
				fmt.Fprintln(os.Stderr, "error: missing value for", a)
				return 2
			}
			i++
			env := map[string]string{"--real-user": "FSTABCTL_REAL_USER", "--backup-dir": "FSTABCTL_BACKUP_DIR",
				"--fstab": "FSTABCTL_FSTAB", "--home": "FSTABCTL_HOME"}[a]
			os.Setenv(env, rest[i])
		case "--":
			args = append(args, rest[i+1:]...)
			i = len(rest)
		default:
			args = append(args, a)
		}
	}
	return RunOp(args, apply, os.Stdout)
}

func parseSize(s string) (int, int, error) {
	w, h, ok := strings.Cut(strings.ToLower(s), "x")
	wi, err1 := strconv.Atoi(w)
	hi, err2 := strconv.Atoi(h)
	if !ok || err1 != nil || err2 != nil || wi < 640 || hi < 480 {
		return 0, 0, fmt.Errorf("--size must look like 1240x820 (at least 640x480)")
	}
	return wi, hi, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "exec" {
		os.Exit(runExec(os.Args[2:]))
	}
	noNotify := flag.Bool("no-notify", false, "don't show desktop notifications when a disk is plugged in")
	size := flag.String("size", "1240x820", "initial window size, WIDTHxHEIGHT")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "fstab Manager %s: manage fstab mounts, backups and home links.\n\nUsage: fstab-manager [options]\n\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("fstab Manager", version)
		return
	}
	w, h, err := parseSize(*size)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	webkitWorkarounds()
	srv, err := newServer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	app := newApp(srv, !*noNotify)
	err = wails.Run(&options.App{
		Title:            "fstab Manager",
		Width:            w,
		Height:           h,
		MinWidth:         860,
		MinHeight:        560,
		BackgroundColour: &options.RGBA{R: 244, G: 246, B: 249, A: 255},
		AssetServer:      &assetserver.Options{Handler: srv.routes()},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "org.fstabctl.manager",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				if app.ctx != nil {
					runtime.WindowUnminimise(app.ctx)
					runtime.WindowShow(app.ctx)
				}
			},
		},
		Linux: &linux.Options{
			Icon:             iconPNG,
			ProgramName:      "fstab-manager",
			WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// webkitWorkarounds avoids known WebKitGTK crashes. Its DMA-BUF renderer and
// accelerated compositing can make the window close at start with
// "Error 71 (Protocol error) dispatching to Wayland display" (seen on KDE
// Plasma with Intel graphics) or stay blank (NVIDIA). This app is a simple page,
// so it turns both off. A user who sets either variable keeps their own value.
func webkitWorkarounds() {
	for _, v := range []string{"WEBKIT_DISABLE_DMABUF_RENDERER", "WEBKIT_DISABLE_COMPOSITING_MODE"} {
		if _, set := os.LookupEnv(v); !set {
			os.Setenv(v, "1")
		}
	}
}
