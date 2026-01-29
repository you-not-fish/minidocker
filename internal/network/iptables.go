//go:build linux
// +build linux

package network

import (
	"fmt"
	"strconv"

	"github.com/coreos/go-iptables/iptables"
)

// iptablesManager 管理 iptables 规则
type iptablesManager struct {
	ipt        *iptables.IPTables
	bridgeName string
	subnet     string
}

// newIptablesManager 创建新的 iptables 管理器
func newIptablesManager(bridgeName string) (*iptablesManager, error) {
	ipt, err := iptables.New()
	if err != nil {
		return nil, fmt.Errorf("create iptables instance: %w", err)
	}

	return &iptablesManager{
		ipt:        ipt,
		bridgeName: bridgeName,
		subnet:     DefaultSubnet,
	}, nil
}

// SetupMasquerade 设置 MASQUERADE 规则用于出网 NAT
// iptables -t nat -A POSTROUTING -s 172.17.0.0/16 ! -o minidocker0 -j MASQUERADE
func (m *iptablesManager) SetupMasquerade() error {
	ruleSpec := []string{
		"-s", m.subnet,
		"!", "-o", m.bridgeName,
		"-j", "MASQUERADE",
	}

	// 使用 AppendUnique 确保规则只添加一次
	exists, err := m.ipt.Exists("nat", "POSTROUTING", ruleSpec...)
	if err != nil {
		return fmt.Errorf("check masquerade rule: %w", err)
	}

	if !exists {
		if err := m.ipt.Append("nat", "POSTROUTING", ruleSpec...); err != nil {
			return fmt.Errorf("add masquerade rule: %w", err)
		}
	}

	return nil
}

// TeardownMasquerade 移除 MASQUERADE 规则
func (m *iptablesManager) TeardownMasquerade() error {
	ruleSpec := []string{
		"-s", m.subnet,
		"!", "-o", m.bridgeName,
		"-j", "MASQUERADE",
	}

	exists, err := m.ipt.Exists("nat", "POSTROUTING", ruleSpec...)
	if err != nil {
		return fmt.Errorf("check masquerade rule: %w", err)
	}

	if exists {
		if err := m.ipt.Delete("nat", "POSTROUTING", ruleSpec...); err != nil {
			return fmt.Errorf("delete masquerade rule: %w", err)
		}
	}

	return nil
}

// SetupPortMapping 设置端口映射规则
// DNAT 规则：iptables -t nat -A PREROUTING -p tcp --dport <hostPort> -j DNAT --to <containerIP>:<containerPort>
// FORWARD 规则：iptables -A FORWARD -p tcp -d <containerIP> --dport <containerPort> -j ACCEPT
func (m *iptablesManager) SetupPortMapping(containerIP string, mapping PortMapping) error {
	protocol := mapping.GetProtocol()
	hostIP := mapping.GetHostIP()
	hostPort := strconv.Itoa(int(mapping.HostPort))
	containerPort := strconv.Itoa(int(mapping.ContainerPort))
	destination := fmt.Sprintf("%s:%s", containerIP, containerPort)

	// PREROUTING DNAT 规则（处理从外部进入的流量）
	// 限制为目的地址为本机（LOCAL），避免错误劫持宿主访问远端的流量。
	preroutingRule := []string{
		"-p", protocol,
		"-m", protocol,
	}
	if hostIP != "" && hostIP != "0.0.0.0" {
		preroutingRule = append(preroutingRule, "-d", hostIP)
	}
	preroutingRule = append(preroutingRule,
		"-m", "addrtype",
		"--dst-type", "LOCAL",
		"--dport", hostPort,
		"-j", "DNAT",
		"--to-destination", destination,
	)

	exists, err := m.ipt.Exists("nat", "PREROUTING", preroutingRule...)
	if err != nil {
		return fmt.Errorf("check PREROUTING rule: %w", err)
	}

	addedPrerouting := false
	if !exists {
		if err := m.ipt.Append("nat", "PREROUTING", preroutingRule...); err != nil {
			return fmt.Errorf("add PREROUTING rule: %w", err)
		}
		addedPrerouting = true
	}

	// OUTPUT DNAT 规则（处理从本地发起到 localhost 的流量）
	// 同样限制为目的地址为本机（LOCAL），避免将宿主到远端 :hostPort 的连接 DNAT 到容器。
	outputRule := []string{
		"-p", protocol,
		"-m", protocol,
	}
	if hostIP != "" && hostIP != "0.0.0.0" {
		outputRule = append(outputRule, "-d", hostIP)
	}
	outputRule = append(outputRule,
		"-m", "addrtype",
		"--dst-type", "LOCAL",
		"--dport", hostPort,
		"-j", "DNAT",
		"--to-destination", destination,
	)

	exists, err = m.ipt.Exists("nat", "OUTPUT", outputRule...)
	if err != nil {
		if addedPrerouting {
			_ = m.ipt.Delete("nat", "PREROUTING", preroutingRule...)
		}
		return fmt.Errorf("check OUTPUT rule: %w", err)
	}

	addedOutput := false
	if !exists {
		if err := m.ipt.Append("nat", "OUTPUT", outputRule...); err != nil {
			if addedPrerouting {
				_ = m.ipt.Delete("nat", "PREROUTING", preroutingRule...)
			}
			return fmt.Errorf("add OUTPUT rule: %w", err)
		}
		addedOutput = true
	}

	// FORWARD ACCEPT 规则（允许转发到容器）
	forwardRule := []string{
		"-p", protocol,
		"-d", containerIP,
		"--dport", containerPort,
		"-j", "ACCEPT",
	}

	exists, err = m.ipt.Exists("filter", "FORWARD", forwardRule...)
	if err != nil {
		if addedOutput {
			_ = m.ipt.Delete("nat", "OUTPUT", outputRule...)
		}
		if addedPrerouting {
			_ = m.ipt.Delete("nat", "PREROUTING", preroutingRule...)
		}
		return fmt.Errorf("check FORWARD rule: %w", err)
	}

	addedForward := false
	if !exists {
		if err := m.ipt.Append("filter", "FORWARD", forwardRule...); err != nil {
			if addedOutput {
				_ = m.ipt.Delete("nat", "OUTPUT", outputRule...)
			}
			if addedPrerouting {
				_ = m.ipt.Delete("nat", "PREROUTING", preroutingRule...)
			}
			return fmt.Errorf("add FORWARD rule: %w", err)
		}
		addedForward = true
	}

	_ = addedForward // reserved for future: if we add more rules, keep symmetry
	return nil
}

// TeardownPortMapping 移除端口映射规则
func (m *iptablesManager) TeardownPortMapping(containerIP string, mapping PortMapping) error {
	protocol := mapping.GetProtocol()
	hostIP := mapping.GetHostIP()
	hostPort := strconv.Itoa(int(mapping.HostPort))
	containerPort := strconv.Itoa(int(mapping.ContainerPort))
	destination := fmt.Sprintf("%s:%s", containerIP, containerPort)

	// 删除 PREROUTING DNAT 规则
	preroutingRule := []string{
		"-p", protocol,
		"-m", protocol,
	}
	if hostIP != "" && hostIP != "0.0.0.0" {
		preroutingRule = append(preroutingRule, "-d", hostIP)
	}
	preroutingRule = append(preroutingRule,
		"-m", "addrtype",
		"--dst-type", "LOCAL",
		"--dport", hostPort,
		"-j", "DNAT",
		"--to-destination", destination,
	)

	exists, err := m.ipt.Exists("nat", "PREROUTING", preroutingRule...)
	if err == nil && exists {
		_ = m.ipt.Delete("nat", "PREROUTING", preroutingRule...)
	}

	// 删除 OUTPUT DNAT 规则
	outputRule := []string{
		"-p", protocol,
		"-m", protocol,
	}
	if hostIP != "" && hostIP != "0.0.0.0" {
		outputRule = append(outputRule, "-d", hostIP)
	}
	outputRule = append(outputRule,
		"-m", "addrtype",
		"--dst-type", "LOCAL",
		"--dport", hostPort,
		"-j", "DNAT",
		"--to-destination", destination,
	)

	exists, err = m.ipt.Exists("nat", "OUTPUT", outputRule...)
	if err == nil && exists {
		_ = m.ipt.Delete("nat", "OUTPUT", outputRule...)
	}

	// 删除 FORWARD ACCEPT 规则
	forwardRule := []string{
		"-p", protocol,
		"-d", containerIP,
		"--dport", containerPort,
		"-j", "ACCEPT",
	}

	exists, err = m.ipt.Exists("filter", "FORWARD", forwardRule...)
	if err == nil && exists {
		_ = m.ipt.Delete("filter", "FORWARD", forwardRule...)
	}

	return nil
}

// SetupForwardAccept 设置允许 bridge 流量转发的规则
func (m *iptablesManager) SetupForwardAccept() error {
	// 允许从 bridge 出去的流量
	outRule := []string{
		"-i", m.bridgeName,
		"-j", "ACCEPT",
	}

	exists, err := m.ipt.Exists("filter", "FORWARD", outRule...)
	if err != nil {
		return fmt.Errorf("check FORWARD out rule: %w", err)
	}

	if !exists {
		if err := m.ipt.Append("filter", "FORWARD", outRule...); err != nil {
			return fmt.Errorf("add FORWARD out rule: %w", err)
		}
	}

	// 允许进入 bridge 的流量
	inRule := []string{
		"-o", m.bridgeName,
		"-j", "ACCEPT",
	}

	exists, err = m.ipt.Exists("filter", "FORWARD", inRule...)
	if err != nil {
		return fmt.Errorf("check FORWARD in rule: %w", err)
	}

	if !exists {
		if err := m.ipt.Append("filter", "FORWARD", inRule...); err != nil {
			return fmt.Errorf("add FORWARD in rule: %w", err)
		}
	}

	return nil
}
