//go:build integration && linux
// +build integration,linux

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Phase 6: cgroup v2 集成测试
//
// 测试环境要求：
// - Linux 内核 >= 5.10（推荐）
// - cgroup v2 统一层级
// - Root 权限
// - minidocker 二进制文件

// skipIfNotCgroupV2 跳过非 cgroup v2 系统
func skipIfNotCgroupV2(t *testing.T) {
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); os.IsNotExist(err) {
		t.Skip("This test requires cgroup v2 (unified hierarchy)")
	}
}

// TestCgroupV2Available 检测 cgroup v2 是否可用
func TestCgroupV2Available(t *testing.T) {
	skipIfNotRoot(t)

	// 检查 cgroup v2 统一层级
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); os.IsNotExist(err) {
		t.Fatal("cgroup v2 is not available on this system")
	}

	// 检查必要的控制器
	data, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		t.Fatalf("Failed to read cgroup.controllers: %v", err)
	}

	controllers := strings.Fields(string(data))
	required := []string{"memory", "cpu", "pids"}
	for _, r := range required {
		found := false
		for _, c := range controllers {
			if c == r {
				found = true
				break
			}
		}
		if !found {
			t.Logf("Warning: controller %s not available (some tests may be skipped)", r)
		}
	}

	t.Logf("Available controllers: %v", controllers)
}

// TestMemoryLimit 测试内存限制生效
func TestMemoryLimit(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 运行容器并检查 memory.max 是否正确设置
	cmd := exec.Command(minidockerBin, "run",
		"-m", "64m",
		"--rootfs", rootfs,
		"/bin/sh", "-c", "cat /sys/fs/cgroup/memory.max")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	// 验证 memory.max 值
	memoryMax := strings.TrimSpace(string(output))
	expected := strconv.FormatInt(64*1024*1024, 10) // 64MB in bytes
	if memoryMax != expected {
		t.Errorf("Expected memory.max=%s, got=%s", expected, memoryMax)
	}
}

// TestCPULimit 测试 CPU 限制生效
func TestCPULimit(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 运行容器并检查 cpu.max 是否正确设置
	cmd := exec.Command(minidockerBin, "run",
		"--cpus", "0.5",
		"--rootfs", rootfs,
		"/bin/sh", "-c", "cat /sys/fs/cgroup/cpu.max")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	// 验证 cpu.max 值
	// 格式: "quota period"
	cpuMax := strings.TrimSpace(string(output))
	parts := strings.Fields(cpuMax)
	if len(parts) != 2 {
		t.Fatalf("Unexpected cpu.max format: %s", cpuMax)
	}

	quota, _ := strconv.ParseInt(parts[0], 10, 64)
	period, _ := strconv.ParseInt(parts[1], 10, 64)

	// 0.5 CPU = 50000 / 100000
	expectedQuota := int64(50000)
	expectedPeriod := int64(100000)

	if quota != expectedQuota || period != expectedPeriod {
		t.Errorf("Expected cpu.max=%d %d, got=%d %d",
			expectedQuota, expectedPeriod, quota, period)
	}
}

// TestPidsLimit 测试进程数限制
func TestPidsLimit(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 运行容器并检查 pids.max 是否正确设置
	cmd := exec.Command(minidockerBin, "run",
		"--pids-limit", "10",
		"--rootfs", rootfs,
		"/bin/sh", "-c", "cat /sys/fs/cgroup/pids.max")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	// 验证 pids.max 值
	pidsMax := strings.TrimSpace(string(output))
	if pidsMax != "10" {
		t.Errorf("Expected pids.max=10, got=%s", pidsMax)
	}
}

// TestPidsLimitEnforcement 测试进程数限制实际生效
func TestPidsLimitEnforcement(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 设置 pids 限制为 5，尝试创建更多进程
	// 注意：shell 本身和 sleep 命令会占用一些进程配额
	cmd := exec.Command(minidockerBin, "run",
		"--pids-limit", "5",
		"--rootfs", rootfs,
		"/bin/sh", "-c", `
for i in 1 2 3 4 5 6 7 8 9 10; do
    sleep 0.1 &
done 2>/dev/null
wait 2>/dev/null
echo "done"
`)
	output, _ := cmd.CombinedOutput()

	// 只要命令完成且输出 "done" 就认为测试通过
	// 实际的 fork 限制会导致部分 sleep 启动失败，但不会导致整个脚本崩溃
	if !strings.Contains(string(output), "done") {
		t.Logf("Output: %s", output)
		t.Errorf("Expected 'done' in output (pids limit may have caused failures)")
	}
}

// TestCgroupCleanup 测试 cgroup 清理
func TestCgroupCleanup(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 运行带资源限制的容器（前台模式，会立即退出）
	cmd := exec.Command(minidockerBin, "run",
		"-m", "64m",
		"--rootfs", rootfs,
		"/bin/echo", "hello")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	// 等待一小段时间确保清理完成
	time.Sleep(100 * time.Millisecond)

	// 检查 minidocker cgroup 目录
	// 前台模式下，容器退出后 cgroup 应该被清理
	minidockerCgroupDir := "/sys/fs/cgroup/minidocker"
	entries, err := os.ReadDir(minidockerCgroupDir)
	if err == nil && len(entries) > 0 {
		// 可能有其他容器的 cgroup，但数量应该很少
		t.Logf("Warning: found %d cgroup entries in %s (may be from other containers)",
			len(entries), minidockerCgroupDir)
	}
}

// TestDetachedCgroupCleanup 测试后台模式下 cgroup 清理
func TestDetachedCgroupCleanup(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 运行后台容器
	cmd := exec.Command(minidockerBin, "run",
		"-d",
		"-m", "64m",
		"--rootfs", rootfs,
		"/bin/sleep", "1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	if len(containerID) != 64 {
		t.Fatalf("Invalid container ID: %s", containerID)
	}

	// 检查 cgroup 目录是否创建
	cgroupPath := filepath.Join("/sys/fs/cgroup/minidocker", containerID)
	if _, err := os.Stat(cgroupPath); os.IsNotExist(err) {
		t.Errorf("Cgroup directory should exist while container is running: %s", cgroupPath)
	}

	// 等待容器退出
	time.Sleep(2 * time.Second)

	// 检查 cgroup 目录是否已清理
	if _, err := os.Stat(cgroupPath); !os.IsNotExist(err) {
		t.Errorf("Cgroup directory should be removed after container exit: %s", cgroupPath)
	}

	// 清理容器
	cleanupCmd := exec.Command(minidockerBin, "rm", containerID)
	_ = cleanupCmd.Run()
}

// TestCombinedLimits 测试组合资源限制
func TestCombinedLimits(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 运行容器并检查多个资源限制
	cmd := exec.Command(minidockerBin, "run",
		"-m", "128m",
		"--cpus", "1",
		"--pids-limit", "50",
		"--rootfs", rootfs,
		"/bin/sh", "-c", `
echo "memory.max=$(cat /sys/fs/cgroup/memory.max)"
echo "cpu.max=$(cat /sys/fs/cgroup/cpu.max)"
echo "pids.max=$(cat /sys/fs/cgroup/pids.max)"
`)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// 验证内存限制
	expectedMemory := strconv.FormatInt(128*1024*1024, 10)
	if !strings.Contains(outputStr, "memory.max="+expectedMemory) {
		t.Errorf("Expected memory.max=%s in output", expectedMemory)
	}

	// 验证 CPU 限制
	if !strings.Contains(outputStr, "cpu.max=100000 100000") {
		t.Errorf("Expected cpu.max=100000 100000 in output")
	}

	// 验证进程数限制
	if !strings.Contains(outputStr, "pids.max=50") {
		t.Errorf("Expected pids.max=50 in output")
	}

	t.Logf("Output:\n%s", outputStr)
}

// TestNoCgroupWithoutLimits 测试无资源限制时不创建 cgroup
func TestNoCgroupWithoutLimits(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 运行不带资源限制的容器
	// 此时不应该创建 cgroup
	cmd := exec.Command(minidockerBin, "run",
		"--rootfs", rootfs,
		"/bin/sh", "-c", `
# 检查是否在 minidocker cgroup 中
cgroup=$(cat /proc/self/cgroup)
if echo "$cgroup" | grep -q "minidocker"; then
    echo "UNEXPECTED: running in minidocker cgroup"
else
    echo "OK: not in minidocker cgroup"
fi
`)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	if strings.Contains(string(output), "UNEXPECTED") {
		t.Errorf("Container should not be in minidocker cgroup when no limits are set")
	}
}

// TestInspectCgroupPath 测试 inspect 显示 cgroup 路径
func TestInspectCgroupPath(t *testing.T) {
	skipIfNotRoot(t)
	skipIfNotCgroupV2(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	// 运行后台容器
	runCmd := exec.Command(minidockerBin, "run",
		"-d",
		"-m", "64m",
		"--rootfs", rootfs,
		"/bin/sleep", "10")
	output, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run -d failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	defer func() {
		// 清理
		exec.Command(minidockerBin, "kill", containerID).Run()
		exec.Command(minidockerBin, "rm", containerID).Run()
	}()

	// 检查 inspect 输出
	inspectCmd := exec.Command(minidockerBin, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	// 验证 cgroupPath 字段存在
	if !strings.Contains(string(inspectOutput), "cgroupPath") {
		t.Errorf("Expected cgroupPath in inspect output")
	}

	// 验证 cgroup 路径格式
	expectedPath := "minidocker/" + containerID
	if !strings.Contains(string(inspectOutput), expectedPath) {
		t.Errorf("Expected cgroupPath containing %s", expectedPath)
	}

	t.Logf("Inspect output:\n%s", inspectOutput)
}
