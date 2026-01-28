//go:build integration && linux
// +build integration,linux

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Phase 5 集成测试：exec 命令
//
// 测试覆盖：
// - 基本 exec 功能
// - 命名空间隔离验证
// - 容器内 PID 验证
// - 退出码传播
// - 容器状态验证（必须运行中）
// - 短 ID 支持
// - exec 不影响容器生命周期
// - PTY 模式基本测试

// ==================== EXEC 命令测试 ====================

// TestExecBasic 测试基本 exec 功能
func TestExecBasic(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器启动
	time.Sleep(300 * time.Millisecond)

	// 执行命令
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID, "/bin/echo", "hello_exec")
	execOutput, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec failed: %v\nOutput: %s", err, execOutput)
	}

	if !strings.Contains(string(execOutput), "hello_exec") {
		t.Errorf("Expected 'hello_exec' in output, got: %s", execOutput)
	}
}

// TestExecNamespaceIsolation 验证 exec 在容器命名空间中运行
func TestExecNamespaceIsolation(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 获取容器的 UTS namespace（主机名）
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID, "/bin/hostname")
	execOutput, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec failed: %v\nOutput: %s", err, execOutput)
	}
	containerHostname := strings.TrimSpace(string(execOutput))

	// 获取宿主机主机名
	hostHostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("get host hostname failed: %v", err)
	}

	// 容器主机名应该与宿主机不同（应该是容器 ID 的前 12 位）
	if containerHostname == hostHostname {
		t.Errorf("exec should run in container's UTS namespace, container hostname should differ from host")
	}

	// 容器主机名应该是容器 ID 的前 12 位
	if containerHostname != containerID[:12] {
		t.Errorf("Expected container hostname to be %s, got: %s", containerID[:12], containerHostname)
	}
}

// TestExecMountNamespace 验证 exec 在容器的 mount namespace 中运行
func TestExecMountNamespace(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 验证容器根目录与宿主机不同
	// 容器内应该看不到宿主机的 /etc/os-release（如果存在）
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID, "/bin/ls", "/")
	execOutput, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec ls / failed: %v\nOutput: %s", err, execOutput)
	}

	// 容器根目录应该只包含 rootfs 中的内容
	rootContents := string(execOutput)
	if !strings.Contains(rootContents, "bin") {
		t.Errorf("Expected 'bin' in container root, got: %s", rootContents)
	}
}

// TestExecPIDInContainer 验证 exec 看到容器的进程树
func TestExecPIDInContainer(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 可靠验证：exec 进程应处于与容器 init 相同的 PID namespace
	containerPID := readContainerPIDFromState(t, stateRoot, containerID)
	expectedNS, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/pid", containerPID))
	if err != nil {
		t.Fatalf("readlink pid namespace failed: %v", err)
	}

	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID, "/bin/sh", "-c", "cat /proc/self/ns/pid")
	execOutput, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec failed: %v\nOutput: %s", err, execOutput)
	}

	gotNS := strings.TrimSpace(string(execOutput))
	if gotNS != expectedNS {
		t.Errorf("expected pid namespace %q, got %q", expectedNS, gotNS)
	}
}

// TestExecExitCode 验证退出码传播
func TestExecExitCode(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	tests := []struct {
		name     string
		cmd      []string
		expected int
	}{
		{"success", []string{"/bin/true"}, 0},
		{"failure", []string{"/bin/false"}, 1},
		{"custom_42", []string{"/bin/sh", "-c", "exit 42"}, 42},
		{"custom_100", []string{"/bin/sh", "-c", "exit 100"}, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--root", stateRoot, "exec", containerID}, tt.cmd...)
			execCmd := exec.Command(minidockerBin, args...)
			err := execCmd.Run()

			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("unexpected error: %v", err)
				}
			}

			if exitCode != tt.expected {
				t.Errorf("expected exit code %d, got %d", tt.expected, exitCode)
			}
		})
	}
}

// TestExecNotRunning 验证对非运行容器 exec 会报错
func TestExecNotRunning(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动并等待快速退出的容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/true")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	// 等待容器退出
	time.Sleep(500 * time.Millisecond)

	// 尝试对已停止容器执行 exec
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID, "/bin/echo", "hello")
	execOutput, err := execCmd.CombinedOutput()
	if err == nil {
		t.Error("expected error when exec on stopped container")
	}
	if !strings.Contains(string(execOutput), "not running") {
		t.Errorf("expected 'not running' error, got: %s", execOutput)
	}
}

// TestExecContainerNotFound 验证对不存在容器 exec 会报错
func TestExecContainerNotFound(t *testing.T) {
	skipIfNotRoot(t)

	stateRoot := t.TempDir()

	// 尝试对不存在的容器执行 exec
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", "nonexistent123", "/bin/echo", "hello")
	execOutput, err := execCmd.CombinedOutput()
	if err == nil {
		t.Error("expected error when exec on non-existent container")
	}
	if !strings.Contains(string(execOutput), "not found") {
		t.Errorf("expected 'not found' error, got: %s", execOutput)
	}
}

// TestExecWithShortID 验证短 ID 支持
func TestExecWithShortID(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))
	shortID := containerID[:12]

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 使用短 ID 执行 exec
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", shortID, "/bin/echo", "short_id_works")
	execOutput, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec with short ID failed: %v\nOutput: %s", err, execOutput)
	}

	if !strings.Contains(string(execOutput), "short_id_works") {
		t.Errorf("Expected 'short_id_works', got: %s", execOutput)
	}
}

// TestExecDoesNotAffectContainer 验证 exec 退出不影响容器
func TestExecDoesNotAffectContainer(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 执行多个会退出的命令
	for i := 0; i < 5; i++ {
		execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID, "/bin/true")
		if _, err := execCmd.CombinedOutput(); err != nil {
			t.Fatalf("exec %d failed: %v", i, err)
		}
	}

	// 执行一个会失败的命令
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID, "/bin/false")
	execCmd.Run() // 忽略错误，我们只关心容器是否仍在运行

	// 等待一小段时间
	time.Sleep(200 * time.Millisecond)

	// 容器应该仍在运行
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}

	if !strings.Contains(string(inspectOutput), "running") {
		t.Error("Container should still be running after exec exits")
	}
}

// TestExecMultipleCommands 测试连续执行多个命令
func TestExecMultipleCommands(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 执行多个不同的命令
	commands := []struct {
		cmd      []string
		expected string
	}{
		{[]string{"/bin/echo", "first"}, "first"},
		{[]string{"/bin/echo", "second"}, "second"},
		{[]string{"/bin/sh", "-c", "echo third"}, "third"},
	}

	for i, c := range commands {
		args := append([]string{"--root", stateRoot, "exec", containerID}, c.cmd...)
		execCmd := exec.Command(minidockerBin, args...)
		execOutput, err := execCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("exec %d failed: %v\nOutput: %s", i, err, execOutput)
		}

		if !strings.Contains(string(execOutput), c.expected) {
			t.Errorf("exec %d: expected '%s', got: %s", i, c.expected, execOutput)
		}
	}
}

// TestExecCommandNotFound 测试执行不存在的命令
func TestExecCommandNotFound(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 执行不存在的命令
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID, "/nonexistent/command")
	err = execCmd.Run()

	if err == nil {
		t.Error("expected error when executing non-existent command")
	}

	// 退出码应该是 127（命令未找到的标准退出码）
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 127 {
			t.Errorf("expected exit code 127 for command not found, got: %d", exitErr.ExitCode())
		}
	}
}

// TestExecWithArgs 测试带参数的命令执行
func TestExecWithArgs(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 执行带多个参数的 shell 命令
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID,
		"/bin/sh", "-c", "echo arg1=$1 arg2=$2", "--", "value1", "value2")
	execOutput, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec failed: %v\nOutput: %s", err, execOutput)
	}

	if !strings.Contains(string(execOutput), "arg1=value1") || !strings.Contains(string(execOutput), "arg2=value2") {
		t.Errorf("Expected arguments to be passed correctly, got: %s", execOutput)
	}
}

// TestExecIPCNamespace 验证 exec 在容器的 IPC namespace 中运行
func TestExecIPCNamespace(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 获取容器的 IPC namespace
	execCmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", containerID,
		"/bin/sh", "-c", "cat /proc/self/ns/ipc")
	execOutput, err := execCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("exec failed: %v\nOutput: %s", err, execOutput)
	}
	containerIPCNS := strings.TrimSpace(string(execOutput))

	// 获取宿主机的 IPC namespace
	hostIPCNS, err := os.Readlink("/proc/self/ns/ipc")
	if err != nil {
		t.Fatalf("readlink failed: %v", err)
	}

	// 应该不同
	if containerIPCNS == hostIPCNS {
		t.Error("exec should run in container's IPC namespace, not host")
	}
}

// TestExecWithPTYBasic 基本验证 -it PTY 模式可交互工作。
// 说明：完整的 TTY/resize/ctrl-c 行为测试较复杂，这里做一个最小闭环：
// - 启动 `minidocker exec -it <id> /bin/sh`
// - 通过一个外层 PTY 写入命令并断言输出包含预期内容
func TestExecWithPTYBasic(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	runCmd := exec.Command(minidockerBin, "--root", stateRoot, "run", "-d", "--rootfs", rootfs, "/bin/sleep", "100")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\nOutput: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	t.Cleanup(func() {
		exec.Command(minidockerBin, "--root", stateRoot, "kill", containerID).Run()
		exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID).Run()
	})

	time.Sleep(300 * time.Millisecond)

	// 外层 PTY：用于与 minidocker exec 的 stdin/stdout 交互
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "exec", "-it", containerID, "/bin/sh")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start pty failed: %v", err)
	}
	defer ptmx.Close()

	// 收集输出
	var buf bytes.Buffer
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, ptmx)
		close(copyDone)
	}()

	// 写入命令并退出
	_, _ = ptmx.Write([]byte("echo pty_ok\nexit\n"))

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	select {
	case err := <-waitDone:
		// 等待输出 goroutine 收尾
		select {
		case <-copyDone:
		case <-time.After(2 * time.Second):
			// best-effort
		}

		out := buf.String()
		if !strings.Contains(out, "pty_ok") {
			t.Fatalf("expected output to contain %q, got: %s", "pty_ok", out)
		}

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				t.Fatalf("exec -it exited with code %d. Output: %s", exitErr.ExitCode(), out)
			}
			t.Fatalf("exec -it failed: %v. Output: %s", err, out)
		}
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("timeout waiting for exec -it to finish. Output so far: %s", buf.String())
	}
}

func readContainerPIDFromState(t *testing.T, stateRoot, containerID string) int {
	t.Helper()

	stateFile := filepath.Join(stateRoot, "containers", containerID, "state.json")
	stateData, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("failed to read state.json: %v", err)
	}

	var st stateJSON
	if err := json.Unmarshal(stateData, &st); err != nil {
		t.Fatalf("failed to parse state.json: %v", err)
	}
	if st.Pid <= 0 {
		t.Fatalf("invalid pid in state.json: %d", st.Pid)
	}
	return st.Pid
}
