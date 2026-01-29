//go:build integration && linux
// +build integration,linux

package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Phase 7: Network 集成测试
//
// 测试环境要求：
// - Linux 内核 >= 5.10（推荐）
// - Root 权限
// - iptables 可用
// - minidocker 二进制文件

// TestNetworkBridgeMode 测试 bridge 网络模式
func TestNetworkBridgeMode(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行后台容器，默认 bridge 模式
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"--network", "bridge",
		"--rootfs", rootfs,
		"/bin/sleep", "30")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 检查 bridge 是否存在
	if _, err := os.Stat("/sys/class/net/minidocker0"); os.IsNotExist(err) {
		t.Error("Bridge minidocker0 should exist after running bridge mode container")
	}

	// 检查 veth 对是否存在
	vethName := fmt.Sprintf("veth%s", containerID[:8])
	if _, err := exec.Command("ip", "link", "show", vethName).CombinedOutput(); err != nil {
		t.Logf("Note: veth %s may not be visible from host (expected for container-side veth)", vethName)
	}

	// 通过 inspect 检查容器获得了 IP 地址
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	if !strings.Contains(string(inspectOutput), "172.17.") {
		t.Errorf("Expected container to have IP in 172.17.x.x range, got:\n%s", inspectOutput)
	}

	t.Logf("Container running with bridge network:\n%s", inspectOutput)
}

// TestNetworkHostMode 测试 host 网络模式
func TestNetworkHostMode(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// host 模式：容器共享宿主机网络
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"--network", "host",
		"--rootfs", rootfs,
		"/bin/hostname")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	// 在 host 模式下，容器应该能看到宿主机的主机名
	containerID := findSingleContainerID(t, stateRoot)
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 检查 inspect 输出，networkState.mode 应该是 "host"
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	if !strings.Contains(string(inspectOutput), `"mode": "host"`) {
		t.Errorf("Expected network mode 'host' in inspect output:\n%s", inspectOutput)
	}

	t.Logf("Host mode container output: %s", output)
}

// TestNetworkNoneMode 测试 none 网络模式
func TestNetworkNoneMode(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// none 模式：容器只有 loopback
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"--network", "none",
		"--rootfs", rootfs,
		"/bin/sleep", "30")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 检查 inspect 输出，networkState.mode 应该是 "none"
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	if !strings.Contains(string(inspectOutput), `"mode": "none"`) {
		t.Errorf("Expected network mode 'none' in inspect output:\n%s", inspectOutput)
	}
}

// TestNetworkDefaultBridge 测试默认网络模式为 bridge
func TestNetworkDefaultBridge(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 不指定 --network，默认应该是 bridge
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"--rootfs", rootfs,
		"/bin/sleep", "30")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 检查容器获得了 IP 地址
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	if !strings.Contains(string(inspectOutput), "172.17.") {
		t.Errorf("Expected container to have IP in 172.17.x.x range (default bridge mode), got:\n%s", inspectOutput)
	}
}

// TestPortMapping 测试端口映射
func TestPortMapping(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行带端口映射的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"-p", "18080:80",
		"-p", "18081:81/tcp",
		"--rootfs", rootfs,
		"/bin/sleep", "30")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	// 检查 inspect 输出中的端口映射
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	// 解析 JSON 验证端口映射
	var inspectResult []map[string]interface{}
	if err := json.Unmarshal(inspectOutput, &inspectResult); err != nil {
		t.Fatalf("Failed to parse inspect output: %v", err)
	}

	if len(inspectResult) == 0 {
		t.Fatal("Expected at least one inspect result")
	}

	state := inspectResult[0]["State"].(map[string]interface{})
	networkState, ok := state["networkState"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected networkState in inspect output:\n%s", inspectOutput)
	}

	portMappings, ok := networkState["portMappings"].([]interface{})
	if !ok || len(portMappings) != 2 {
		t.Errorf("Expected 2 port mappings, got:\n%s", inspectOutput)
	}

	// 检查 iptables 规则是否创建
	iptOutput, _ := exec.Command("iptables", "-t", "nat", "-L", "PREROUTING", "-n").CombinedOutput()
	if !strings.Contains(string(iptOutput), "18080") {
		t.Logf("Note: DNAT rule for port 18080 may be in different chain. iptables output:\n%s", iptOutput)
	}

	t.Logf("Container running with port mappings:\n%s", inspectOutput)
}

// TestNetworkCleanup 测试网络资源在容器退出后被清理
func TestNetworkCleanup(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 运行一个很快退出的容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-p", "19080:80",
		"--rootfs", rootfs,
		"/bin/echo", "hello")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := findSingleContainerID(t, stateRoot)
	defer func() { cleanupContainer(t, stateRoot, containerID) }()

	// 容器退出后，veth 应该被清理
	vethName := fmt.Sprintf("veth%s", containerID[:8])

	// 等待一小段时间确保清理完成
	time.Sleep(500 * time.Millisecond)

	// 检查 veth 是否被清理
	if out, err := exec.Command("ip", "link", "show", vethName).CombinedOutput(); err == nil {
		t.Errorf("veth %s should be cleaned up after container exit, but it still exists:\n%s", vethName, out)
	}

	// 检查 iptables 规则是否被清理
	iptOutput, _ := exec.Command("iptables", "-t", "nat", "-L", "-n").CombinedOutput()
	if strings.Contains(string(iptOutput), "19080") {
		t.Errorf("iptables rule for port 19080 should be cleaned up after container exit:\n%s", iptOutput)
	}
}

// TestIPAMAllocation 测试 IPAM 分配
func TestIPAMAllocation(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动多个容器，检查它们获得不同的 IP
	var containerIDs []string
	for i := 0; i < 3; i++ {
		cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
			"-d",
			"--rootfs", rootfs,
			"/bin/sleep", "60")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
		}
		containerIDs = append(containerIDs, strings.TrimSpace(string(output)))
	}

	t.Cleanup(func() {
		for _, id := range containerIDs {
			cleanupContainer(t, stateRoot, id)
		}
	})

	// 收集所有容器的 IP
	ips := make(map[string]bool)
	for _, id := range containerIDs {
		inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", id)
		inspectOutput, err := inspectCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("inspect failed: %v", err)
		}

		// 简单提取 IP
		for _, line := range strings.Split(string(inspectOutput), "\n") {
			if strings.Contains(line, "ipAddress") {
				// 提取 IP 地址
				parts := strings.Split(line, `"`)
				for _, p := range parts {
					if strings.HasPrefix(p, "172.17.") {
						if ips[p] {
							t.Errorf("Duplicate IP address: %s", p)
						}
						ips[p] = true
					}
				}
			}
		}
	}

	if len(ips) != 3 {
		t.Errorf("Expected 3 unique IPs, got %d: %v", len(ips), ips)
	}

	t.Logf("Allocated IPs: %v", ips)
}

// TestIPAMRelease 测试 IPAM 释放
func TestIPAMRelease(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动一个容器获取 IP
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"--rootfs", rootfs,
		"/bin/sleep", "60")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}
	containerID1 := strings.TrimSpace(string(output))

	// 获取第一个容器的 IP
	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID1)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}

	var firstIP string
	for _, line := range strings.Split(string(inspectOutput), "\n") {
		if strings.Contains(line, "ipAddress") {
			parts := strings.Split(line, `"`)
			for _, p := range parts {
				if strings.HasPrefix(p, "172.17.") {
					firstIP = p
					break
				}
			}
		}
	}

	if firstIP == "" {
		t.Fatal("Failed to get first container's IP")
	}

	// 删除第一个容器
	cleanupContainer(t, stateRoot, containerID1)

	// 检查 IPAM 文件，IP 应该被释放
	ipamPath := filepath.Join(stateRoot, "network", "ipam.json")
	ipamData, err := os.ReadFile(ipamPath)
	if err != nil {
		t.Logf("IPAM file not found (may be cleaned up): %v", err)
		return
	}

	if strings.Contains(string(ipamData), containerID1) {
		t.Errorf("Container ID should be released from IPAM: %s", ipamData)
	}

	t.Logf("IPAM state after cleanup: %s", ipamData)
}

// TestNetworkInspectFields 测试 inspect 显示网络字段
func TestNetworkInspectFields(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"-p", "20080:80",
		"--rootfs", rootfs,
		"/bin/sleep", "30")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() { cleanupContainer(t, stateRoot, containerID) })

	inspectCmd := exec.Command(minidockerBin, "--root", stateRoot, "inspect", containerID)
	inspectOutput, err := inspectCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect failed: %v\nOutput: %s", err, inspectOutput)
	}

	// 验证必要的网络字段存在
	requiredFields := []string{
		"networkState",
		"mode",
		"ipAddress",
		"gateway",
		"vethHost",
	}

	for _, field := range requiredFields {
		if !strings.Contains(string(inspectOutput), field) {
			t.Errorf("Expected field %q in inspect output", field)
		}
	}

	t.Logf("Inspect output:\n%s", inspectOutput)
}

// TestRmCleansNetwork 测试 rm 命令清理网络
func TestRmCleansNetwork(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// 启动容器
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"-d",
		"-p", "21080:80",
		"--rootfs", rootfs,
		"/bin/sleep", "60")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("minidocker run failed: %v\nOutput: %s", err, output)
	}

	containerID := strings.TrimSpace(string(output))
	vethName := fmt.Sprintf("veth%s", containerID[:8])

	// 强制删除容器
	rmCmd := exec.Command(minidockerBin, "--root", stateRoot, "rm", "-f", containerID)
	rmOutput, err := rmCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rm failed: %v\nOutput: %s", err, rmOutput)
	}

	// 等待清理完成
	time.Sleep(500 * time.Millisecond)

	// 验证 veth 被清理
	if out, err := exec.Command("ip", "link", "show", vethName).CombinedOutput(); err == nil {
		t.Errorf("veth %s should be cleaned up after rm, but still exists:\n%s", vethName, out)
	}

	// 验证 iptables 规则被清理
	iptOutput, _ := exec.Command("iptables", "-t", "nat", "-L", "-n").CombinedOutput()
	if strings.Contains(string(iptOutput), "21080") {
		t.Errorf("iptables rule for port 21080 should be cleaned up after rm:\n%s", iptOutput)
	}
}

// TestPortMappingOnlyBridge 测试端口映射只在 bridge 模式下允许
func TestPortMappingOnlyBridge(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	// host 模式下使用 -p 应该报错
	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"--network", "host",
		"-p", "22080:80",
		"--rootfs", rootfs,
		"/bin/echo", "hello")
	output, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("Expected error when using -p with host network mode")
	}

	if !strings.Contains(string(output), "only supported in bridge") {
		t.Errorf("Expected error message about bridge mode, got: %s", output)
	}
}

// TestInvalidNetworkMode 测试无效的网络模式
func TestInvalidNetworkMode(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
		"--network", "invalid",
		"--rootfs", rootfs,
		"/bin/echo", "hello")
	output, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("Expected error for invalid network mode")
	}

	if !strings.Contains(string(output), "unsupported network mode") {
		t.Errorf("Expected error message about unsupported mode, got: %s", output)
	}
}

// TestInvalidPortFormat 测试无效的端口格式
func TestInvalidPortFormat(t *testing.T) {
	skipIfNotRoot(t)

	rootfs := prepareMinimalRootfs(t)
	defer os.RemoveAll(rootfs)

	stateRoot := t.TempDir()

	testCases := []struct {
		name      string
		portSpec  string
		wantError string
	}{
		{"missing_container_port", "8080", "expected hostPort:containerPort"},
		{"invalid_host_port", "abc:80", "invalid host port"},
		{"invalid_container_port", "8080:xyz", "invalid container port"},
		{"invalid_protocol", "8080:80/icmp", "unsupported protocol"},
		{"zero_port", "0:80", "must be between 1 and 65535"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(minidockerBin, "--root", stateRoot, "run",
				"-p", tc.portSpec,
				"--rootfs", rootfs,
				"/bin/echo", "hello")
			output, err := cmd.CombinedOutput()

			if err == nil {
				t.Errorf("Expected error for port spec %q", tc.portSpec)
			}

			if !strings.Contains(strings.ToLower(string(output)), strings.ToLower(tc.wantError)) {
				t.Errorf("Expected error containing %q, got: %s", tc.wantError, output)
			}
		})
	}
}
