//go:build linux
// +build linux

package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"minidocker/internal/state"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var (
	// logs 命令标志
	logsFollow     bool
	logsTail       string
	logsShowStdout bool
	logsShowStderr bool
	logsTimestamps bool // 预留，Phase 4 不实现
)

var logsCmd = &cobra.Command{
	Use:   "logs [OPTIONS] CONTAINER",
	Short: "获取容器的日志",
	Long: `获取容器的标准输出和标准错误日志。

示例:
  minidocker logs my_container
  minidocker logs -f my_container        # 跟踪日志输出
  minidocker logs --tail 100 my_container # 显示最后 100 行
  minidocker logs --stdout my_container   # 只显示标准输出`,
	Args: cobra.ExactArgs(1),
	RunE: showLogs,
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "跟踪日志输出")
	logsCmd.Flags().StringVarP(&logsTail, "tail", "n", "all", "显示最后 N 行（默认 \"all\"）")
	logsCmd.Flags().BoolVar(&logsShowStdout, "stdout", false, "只显示标准输出")
	logsCmd.Flags().BoolVar(&logsShowStderr, "stderr", false, "只显示标准错误")
	logsCmd.Flags().BoolVarP(&logsTimestamps, "timestamps", "t", false, "显示时间戳（预留，当前未实现）")
}

func showLogs(cmd *cobra.Command, args []string) error {
	containerID := args[0]

	store, err := state.NewStore(rootDir)
	if err != nil {
		return fmt.Errorf("failed to initialize state store: %w", err)
	}

	containerState, err := store.Get(containerID)
	if err != nil {
		return err
	}

	// 预留参数提示：当前不实现时间戳输出
	if logsTimestamps {
		fmt.Fprintln(os.Stderr, "Warning: --timestamps is reserved for future use, currently ignored")
	}

	logDir := containerState.GetLogDir()
	stdoutPath := filepath.Join(logDir, "stdout.log")
	stderrPath := filepath.Join(logDir, "stderr.log")

	// 如果没有指定 --stdout 或 --stderr，则显示两者
	showBoth := !logsShowStdout && !logsShowStderr

	// 解析 tail 参数
	var tailLines int = -1 // -1 表示显示所有
	if logsTail != "all" {
		n, err := strconv.Atoi(logsTail)
		if err != nil {
			return fmt.Errorf("invalid tail value: %s (expected number or \"all\")", logsTail)
		}
		if n < 0 {
			return fmt.Errorf("invalid tail value: %d (must be non-negative)", n)
		}
		tailLines = n
	}

	if logsFollow {
		return followLogs(containerState, stdoutPath, stderrPath, showBoth, logsShowStdout, logsShowStderr, tailLines)
	}

	// 非 follow 模式：读取并输出日志
	if showBoth || logsShowStdout {
		if err := outputLogFile(stdoutPath, tailLines, ""); err != nil {
			// 文件不存在不算错误
			if !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Warning: cannot read stdout.log: %v\n", err)
			}
		}
	}

	if showBoth || logsShowStderr {
		if err := outputLogFile(stderrPath, tailLines, ""); err != nil {
			if !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "Warning: cannot read stderr.log: %v\n", err)
			}
		}
	}

	return nil
}

// outputLogFile 输出日志文件内容
func outputLogFile(path string, tailLines int, prefix string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	if tailLines < 0 {
		// 输出所有内容
		_, err := io.Copy(os.Stdout, file)
		return err
	}

	// 读取最后 N 行
	lines, err := readLastNLines(file, tailLines)
	if err != nil {
		return err
	}

	for _, line := range lines {
		fmt.Print(prefix + line)
	}

	return nil
}

// readLastNLines 读取文件的最后 N 行
func readLastNLines(file *os.File, n int) ([]string, error) {
	if n == 0 {
		return nil, nil
	}

	// 确保从文件头开始读取
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	// 使用 ring buffer 仅保留最后 N 行，避免把整个文件读入内存
	scanner := bufio.NewScanner(file)
	// bufio.Scanner 默认 token 上限较小；提高上限以避免长行导致错误
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	ring := make([]string, n)
	count := 0
	for scanner.Scan() {
		ring[count%n] = scanner.Text() + "\n"
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if count == 0 {
		return nil, nil
	}
	if count < n {
		return ring[:count], nil
	}

	start := count % n
	lines := make([]string, 0, n)
	lines = append(lines, ring[start:]...)
	lines = append(lines, ring[:start]...)
	return lines, nil
}

// followLogs 使用 fsnotify 跟踪日志文件变化
func followLogs(containerState *state.ContainerState, stdoutPath, stderrPath string, showBoth, showStdout, showStderr bool, tailLines int) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer watcher.Close()

	// 打开文件并定位到末尾（先输出 tail 行）
	var stdoutFile, stderrFile *os.File
	var stdoutOffset, stderrOffset int64

	if showBoth || showStdout {
		stdoutFile, stdoutOffset, err = openAndTail(stdoutPath, tailLines)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to open stdout.log: %w", err)
		}
		if stdoutFile != nil {
			defer stdoutFile.Close()
			if err := watcher.Add(stdoutPath); err != nil {
				// 忽略监控添加失败（文件可能不存在）
				fmt.Fprintf(os.Stderr, "Warning: cannot watch stdout.log: %v\n", err)
			}
		}
	}

	if showBoth || showStderr {
		stderrFile, stderrOffset, err = openAndTail(stderrPath, tailLines)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to open stderr.log: %w", err)
		}
		if stderrFile != nil {
			defer stderrFile.Close()
			if err := watcher.Add(stderrPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: cannot watch stderr.log: %v\n", err)
			}
		}
	}

	// 设置信号处理，以便优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 对齐 Docker 行为：容器停止后退出 follow（避免一直挂起直到 Ctrl+C）
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			if event.Op&fsnotify.Write == fsnotify.Write {
				// 文件被写入，读取新内容
				if event.Name == stdoutPath && stdoutFile != nil {
					stdoutOffset = readNewContent(stdoutFile, stdoutOffset, "")
				} else if event.Name == stderrPath && stderrFile != nil {
					stderrOffset = readNewContent(stderrFile, stderrOffset, "")
				}
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)

		case <-ticker.C:
			// 周期性检查容器状态：stopped 后退出
			if err := containerState.Reload(); err == nil {
				// running 状态下进一步用 IsRunning() 触发孤儿检测（ESRCH）
				if containerState.Status == state.StatusRunning && !containerState.IsRunning() {
					if stdoutFile != nil {
						stdoutOffset = readNewContent(stdoutFile, stdoutOffset, "")
					}
					if stderrFile != nil {
						stderrOffset = readNewContent(stderrFile, stderrOffset, "")
					}
					return nil
				}
				if containerState.Status == state.StatusStopped {
					if stdoutFile != nil {
						stdoutOffset = readNewContent(stdoutFile, stdoutOffset, "")
					}
					if stderrFile != nil {
						stderrOffset = readNewContent(stderrFile, stderrOffset, "")
					}
					return nil
				}
			}

		case <-sigChan:
			// 收到中断信号，优雅退出
			return nil
		}
	}
}

// openAndTail 打开文件，输出最后 N 行，返回文件和当前偏移量
func openAndTail(path string, tailLines int) (*os.File, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}

	// 先输出 tail 行
	if tailLines >= 0 {
		lines, err := readLastNLines(file, tailLines)
		if err != nil {
			file.Close()
			return nil, 0, err
		}
		for _, line := range lines {
			fmt.Print(line)
		}
	} else {
		// 输出所有内容
		if _, err := io.Copy(os.Stdout, file); err != nil {
			file.Close()
			return nil, 0, err
		}
	}

	// 获取当前偏移量
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		file.Close()
		return nil, 0, err
	}

	return file, offset, nil
}

// readNewContent 从指定偏移量开始读取新内容
func readNewContent(file *os.File, offset int64, prefix string) int64 {
	// 文件可能被截断（例如 log rotation / truncate）；偏移量超过文件大小时回退
	if info, err := file.Stat(); err == nil {
		if info.Size() < offset {
			offset = 0
		}
	}

	// 定位到上次读取的位置
	_, err := file.Seek(offset, io.SeekStart)
	if err != nil {
		return offset
	}

	// 读取新内容
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			fmt.Print(prefix + line)
		}
		if err != nil {
			break
		}
	}

	// 更新偏移量
	newOffset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return offset
	}

	return newOffset
}
