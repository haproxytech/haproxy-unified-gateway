package process

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/haproxytech/client-native/v6/runtime"
	"github.com/haproxytech/client-native/v6/runtime/options"
	hapi "github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/api"
	"github.com/haproxytech/haproxy-unified-gateway/hug/haproxy/params"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"k8s.io/apimachinery/pkg/util/wait"
)

type directControl struct {
	API               hapi.HAProxyClient
	masterSocket      runtime.Runtime
	logger            *slog.Logger
	Params            params.Params
	useAuxFile        bool
	masterSocketValid bool
}

func newDirectControl(api hapi.HAProxyClient, param params.Params, logger *slog.Logger) *directControl {
	dc := directControl{
		API:    api,
		Params: param,
		logger: logger,
	}
	_, _ = dc.Service("start")

	masterSocketArg := param.MasterSocket
	masterSocket, err := runtime.New(context.Background(), options.MasterSocket(masterSocketArg), options.AllowDelayedStart(time.Minute, time.Second))
	if err != nil {
		dc.logger.LogAttrs(context.Background(), slog.LevelError,
			"failed to initialize master socket",
			logging.LogAttrError(err))
		return &dc
	}
	dc.masterSocketValid = true
	dc.masterSocket = masterSocket

	return &dc
}

func (d *directControl) Service(action string) (string, error) {
	if d.Params.Test {
		d.logger.LogAttrs(context.Background(), slog.LevelInfo,
			fmt.Sprintf("HAProxy would be %sed now", action))
		return "", nil
	}
	var cmd *exec.Cmd
	// if processErr is nil, process variable will automatically
	// hold information about a running Master HAproxy process
	process, processErr := haproxyProcess(d.Params.PIDFile)

	masterSocketArg := d.Params.MasterSocket + ",level,admin"

	switch action {
	case "start":
		if processErr == nil {
			d.logger.LogAttrs(context.Background(), slog.LevelInfo, "haproxy is already running")
			if d.Params.ForceRestart {
				msg, err := d.Service("stop")
				if err != nil {
					return msg, err
				}
				err = d.waitUntilGone()
				if err != nil {
					return "", err
				}
				msg, err = d.Service("start")
				if err != nil {
					return msg, err
				}
			}
			return d.Service("reload")
		}
		cmd = exec.Command(d.Params.HaproxyBinary, "-W", "-S", masterSocketArg, "-f", d.Params.MainCfgFile)
		if d.useAuxFile {
			cmd = exec.Command(d.Params.HaproxyBinary, "-W", "-S", masterSocketArg, "-f", d.Params.MainCfgFile, "-f", d.Params.AuxDir)
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return "", cmd.Start()
	case "stop":
		if processErr != nil {
			d.logger.LogAttrs(context.Background(), slog.LevelError, "haproxy is already stopped")
			return "", processErr
		}
		if err := process.Signal(syscall.SIGUSR1); err != nil {
			return "", err
		}
		return "", nil
	case "reload":
		if d.masterSocketValid {
			msg, err := d.masterSocket.Reload()
			if err == nil {
				d.logger.LogAttrs(context.Background(), slog.LevelDebug, msg)
				return "", d.waitUntilReady()
			}
			d.logger.LogAttrs(context.Background(), slog.LevelError,
				"failed to reload",
				logging.LogAttrError(err))
			return msg, err
		}
		if processErr != nil {
			d.logger.LogAttrs(context.Background(), slog.LevelInfo, "haproxy is not running, trying to start it")
			return d.Service("start")
		}
		return "", nil
	default:
		return "", fmt.Errorf("unknown command '%s'", action)
	}
}

func (d *directControl) UseAuxFile(useAuxFile bool) {
	d.useAuxFile = useAuxFile
}

func (d *directControl) SetAPI(api hapi.HAProxyClient) {
	d.API = api
}

func (d *directControl) waitUntilReady() error {
	runtimeClient := d.API.RuntimeClient()
	return wait.PollUntilContextTimeout(context.Background(), 200*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			info, err := runtimeClient.GetInfo()
			if err == nil {
				if info.Error != "" {
					d.logger.LogAttrs(context.Background(), slog.LevelDebug, "waiting for haproxy runtime socket after reload",
						slog.String("error", info.Error))
					return false, nil
				}
				pid := int64(0)
				if info.Info != nil && info.Info.Pid != nil {
					pid = *info.Info.Pid
				}
				d.logger.LogAttrs(ctx, slog.LevelInfo, "haproxy runtime socket is ready after reload",
					slog.Int64("pid", pid))
				return true, nil
			}
			d.logger.LogAttrs(context.Background(), slog.LevelDebug, "waiting for haproxy runtime socket after reload",
				logging.LogAttrError(err))
			return false, nil
		})
}

func (d *directControl) waitUntilGone() error {
	err := wait.PollUntilContextTimeout(context.Background(), 500*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			_, processErr := haproxyProcess(d.Params.PIDFile)

			// If processErr is NOT nil, the process is gone!
			if processErr != nil {
				d.logger.LogAttrs(ctx, slog.LevelInfo, "haproxy is stopped")
				return true, nil // Condition met, stop polling
			}
			d.logger.LogAttrs(context.Background(), slog.LevelDebug, "haproxy is still running... waiting for it to stop")
			return false, nil // Still running, keep polling
		})
	return err
}
