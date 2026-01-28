//go:build linux
// +build linux

package runtime

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// execWithPTY 在 TTY 模式下执行命令，提供完整的 PTY 支持
func execWithPTY(cmd *exec.Cmd, config *ExecConfig) (int, error) {
	// 使用 PTY 启动命令
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return -1, fmt.Errorf("start pty: %w", err)
	}
	defer ptmx.Close()

	// 处理终端大小调整
	resizeCh := make(chan os.Signal, 1)
	signal.Notify(resizeCh, syscall.SIGWINCH)
	defer signal.Stop(resizeCh)

	go func() {
		for range resizeCh {
			// 将当前终端大小传播到 PTY
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
				// 忽略错误，尽力而为
			}
		}
	}()

	// 初始调整大小
	resizeCh <- syscall.SIGWINCH

	// 在交互模式下将 stdin 设置为原始模式（Ctrl+C 等控制字符会透传到 PTY，由从端决定信号行为）
	if config.Interactive {
		if oldState, err := makeRawTerminal(int(os.Stdin.Fd())); err == nil {
			defer restoreTerminal(int(os.Stdin.Fd()), oldState)
		}
	}

	// stdout 始终透传；stdin 仅在 -i 时透传（符合 docker 的 -i 语义直觉）
	doneOut := make(chan struct{})
	go func() {
		defer close(doneOut)
		_, _ = io.Copy(os.Stdout, ptmx)
	}()

	if config.Interactive {
		// 注意：stdin 读取可能阻塞，不能等待它退出，否则在命令结束后可能 hang。
		go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	}

	// 等待命令完成
	err = cmd.Wait()

	// 关闭 ptmx 以尽快结束 stdout 复制；stdin goroutine 由进程退出自然清理
	_ = ptmx.Close()
	<-doneOut

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Go 的 ExitCode() 在被信号杀死时可能返回 -1；这里统一转换为 shell 惯例 128+signal。
			if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				return 128 + int(ws.Signal()), nil
			}
			return exitErr.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

// makeRawTerminal 将终端切换到 raw mode，并返回旧的 termios 以便恢复。
// 仅 Linux；失败（例如 stdin 不是 TTY）时返回错误，调用方可忽略。
func makeRawTerminal(fd int) (*unix.Termios, error) {
	oldState, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}

	newState := *oldState
	// 基本 raw mode：参考 x/term.MakeRaw 的实现
	newState.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	newState.Oflag &^= unix.OPOST
	newState.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	newState.Cflag &^= unix.CSIZE | unix.PARENB
	newState.Cflag |= unix.CS8
	newState.Cc[unix.VMIN] = 1
	newState.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &newState); err != nil {
		return nil, err
	}
	return oldState, nil
}

func restoreTerminal(fd int, state *unix.Termios) {
	if state == nil {
		return
	}
	_ = unix.IoctlSetTermios(fd, unix.TCSETS, state)
}
