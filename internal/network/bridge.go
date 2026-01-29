//go:build linux
// +build linux

package network

import (
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

// bridgeDriver 实现 bridge 网络模式
type bridgeDriver struct {
	bridgeName string
	subnet     *net.IPNet
	gateway    net.IP
}

// newBridgeDriver 创建新的 bridge 驱动
func newBridgeDriver(bridgeName string) (*bridgeDriver, error) {
	_, subnet, err := net.ParseCIDR(DefaultSubnet)
	if err != nil {
		return nil, fmt.Errorf("parse subnet: %w", err)
	}

	gateway := net.ParseIP(DefaultGateway)
	if gateway == nil {
		return nil, fmt.Errorf("parse gateway: %s", DefaultGateway)
	}

	return &bridgeDriver{
		bridgeName: bridgeName,
		subnet:     subnet,
		gateway:    gateway,
	}, nil
}

// EnsureBridge 确保 bridge 接口存在
func (d *bridgeDriver) EnsureBridge() error {
	// 检查 bridge 是否已存在
	br, err := netlink.LinkByName(d.bridgeName)
	if err != nil {
		// Bridge 不存在，创建它
		br = &netlink.Bridge{
			LinkAttrs: netlink.LinkAttrs{
				Name: d.bridgeName,
			},
		}

		if err := netlink.LinkAdd(br); err != nil {
			return fmt.Errorf("create bridge %s: %w", d.bridgeName, err)
		}

		// 重新获取 bridge（获取完整的 link 信息）
		br, err = netlink.LinkByName(d.bridgeName)
		if err != nil {
			return fmt.Errorf("get bridge after creation: %w", err)
		}
	}

	// 检查是否已有 IP 地址
	addrs, err := netlink.AddrList(br, netlink.FAMILY_V4)
	if err != nil {
		return fmt.Errorf("list bridge addresses: %w", err)
	}

	hasIP := false
	gatewayWithMask := fmt.Sprintf("%s/%d", d.gateway.String(), DefaultSubnetMask)
	for _, addr := range addrs {
		if addr.IPNet.String() == gatewayWithMask {
			hasIP = true
			break
		}
	}

	// 如果没有 IP，添加网关 IP
	if !hasIP {
		addr, err := netlink.ParseAddr(gatewayWithMask)
		if err != nil {
			return fmt.Errorf("parse gateway address: %w", err)
		}

		if err := netlink.AddrAdd(br, addr); err != nil {
			return fmt.Errorf("add address to bridge: %w", err)
		}
	}

	// 启动 bridge 接口
	if err := netlink.LinkSetUp(br); err != nil {
		return fmt.Errorf("bring up bridge: %w", err)
	}

	// 启用 IP forwarding
	if err := enableIPForwarding(); err != nil {
		return fmt.Errorf("enable IP forwarding: %w", err)
	}

	return nil
}

// SetupVeth 创建 veth pair 并配置网络
func (d *bridgeDriver) SetupVeth(containerID string, pid int, containerIP string) (*NetworkState, error) {
	// 生成 veth 名称
	// 宿主机端: veth + containerID 前8位
	// 容器端: 先在宿主机 netns 里用临时唯一名创建，移入容器 netns 后再 rename 为 eth0
	hostVethName := fmt.Sprintf("veth%s", containerID[:8])
	peerVethName := fmt.Sprintf("ceth%s", containerID[:8])
	containerVethName := "eth0"

	// 获取 bridge
	br, err := netlink.LinkByName(d.bridgeName)
	if err != nil {
		return nil, fmt.Errorf("get bridge: %w", err)
	}

	// 创建 veth pair
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: hostVethName,
		},
		PeerName: peerVethName,
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return nil, fmt.Errorf("create veth pair: %w", err)
	}

	// 获取宿主机端 veth
	hostVeth, err := netlink.LinkByName(hostVethName)
	if err != nil {
		d.cleanupVeth(hostVethName)
		return nil, fmt.Errorf("get host veth: %w", err)
	}

	// 获取容器端 veth
	containerVeth, err := netlink.LinkByName(peerVethName)
	if err != nil {
		d.cleanupVeth(hostVethName)
		return nil, fmt.Errorf("get container veth: %w", err)
	}

	// 将宿主机端连接到 bridge
	if err := netlink.LinkSetMaster(hostVeth, br); err != nil {
		d.cleanupVeth(hostVethName)
		return nil, fmt.Errorf("attach veth to bridge: %w", err)
	}

	// 启动宿主机端 veth
	if err := netlink.LinkSetUp(hostVeth); err != nil {
		d.cleanupVeth(hostVethName)
		return nil, fmt.Errorf("bring up host veth: %w", err)
	}

	// 将容器端 veth 移动到容器网络命名空间
	if err := netlink.LinkSetNsPid(containerVeth, pid); err != nil {
		d.cleanupVeth(hostVethName)
		return nil, fmt.Errorf("move veth to container namespace: %w", err)
	}

	// 在容器命名空间中配置网络
	if err := d.configureContainerNetwork(pid, peerVethName, containerVethName, containerIP); err != nil {
		d.cleanupVeth(hostVethName)
		return nil, fmt.Errorf("configure container network: %w", err)
	}

	// 获取容器网卡的 MAC 地址
	macAddress := ""
	containerNs, err := netns.GetFromPid(pid)
	if err == nil {
		defer containerNs.Close()
		// 在容器命名空间中获取 MAC
		err = withNetns(containerNs, func() error {
			link, err := netlink.LinkByName(containerVethName)
			if err != nil {
				return err
			}
			macAddress = link.Attrs().HardwareAddr.String()
			return nil
		})
	}

	return &NetworkState{
		Mode:          NetworkModeBridge,
		IPAddress:     containerIP,
		Gateway:       d.gateway.String(),
		MacAddress:    macAddress,
		VethHost:      hostVethName,
		VethContainer: containerVethName,
	}, nil
}

// configureContainerNetwork 在容器命名空间中配置网络
func (d *bridgeDriver) configureContainerNetwork(pid int, srcVethName, dstVethName, containerIP string) error {
	// 获取容器的网络命名空间
	containerNs, err := netns.GetFromPid(pid)
	if err != nil {
		return fmt.Errorf("get container network namespace: %w", err)
	}
	defer containerNs.Close()

	return withNetns(containerNs, func() error {
		// 获取容器端 veth（移入容器 netns 后名称仍为 srcVethName）
		veth, err := netlink.LinkByName(srcVethName)
		if err != nil {
			return fmt.Errorf("get container veth: %w", err)
		}

		// Rename 为容器内常见接口名（eth0）。在宿主机 netns 中直接创建名为 eth0
		// 容易与宿主已有接口名冲突（尤其是云主机/VM 默认就有 eth0）。
		if srcVethName != dstVethName {
			// rename 前确保为 down（更稳）
			_ = netlink.LinkSetDown(veth)
			if err := netlink.LinkSetName(veth, dstVethName); err != nil {
				return fmt.Errorf("rename container veth %q -> %q: %w", srcVethName, dstVethName, err)
			}

			// 重新按新名称获取 link
			veth, err = netlink.LinkByName(dstVethName)
			if err != nil {
				return fmt.Errorf("get container veth after rename: %w", err)
			}
		}

		// 配置 IP 地址
		addr, err := netlink.ParseAddr(fmt.Sprintf("%s/%d", containerIP, DefaultSubnetMask))
		if err != nil {
			return fmt.Errorf("parse container IP: %w", err)
		}

		if err := netlink.AddrAdd(veth, addr); err != nil {
			return fmt.Errorf("add IP to container veth: %w", err)
		}

		// 启动容器端 veth
		if err := netlink.LinkSetUp(veth); err != nil {
			return fmt.Errorf("bring up container veth: %w", err)
		}

		// 启动 loopback
		lo, err := netlink.LinkByName("lo")
		if err == nil {
			_ = netlink.LinkSetUp(lo)
		}

		// 添加默认路由
		route := &netlink.Route{
			LinkIndex: veth.Attrs().Index,
			Dst:       nil, // 默认路由
			Gw:        d.gateway,
		}
		if err := netlink.RouteReplace(route); err != nil {
			return fmt.Errorf("add default route: %w", err)
		}

		return nil
	})
}

// TeardownVeth 清理 veth pair
func (d *bridgeDriver) TeardownVeth(hostVethName string) error {
	return d.cleanupVeth(hostVethName)
}

// cleanupVeth 删除 veth pair
func (d *bridgeDriver) cleanupVeth(hostVethName string) error {
	veth, err := netlink.LinkByName(hostVethName)
	if err != nil {
		// veth 不存在，视为已清理
		return nil
	}

	return netlink.LinkDel(veth)
}

// enableIPForwarding 启用 IP 转发
func enableIPForwarding() error {
	return os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0644)
}

// withNetns 在指定的网络命名空间中执行函数
func withNetns(ns netns.NsHandle, fn func() error) error {
	// 锁定当前 goroutine 到 OS 线程
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// 保存当前网络命名空间
	currentNs, err := netns.Get()
	if err != nil {
		return fmt.Errorf("get current network namespace: %w", err)
	}
	defer currentNs.Close()

	// 切换到目标网络命名空间
	if err := netns.Set(ns); err != nil {
		return fmt.Errorf("set network namespace: %w", err)
	}

	// 执行函数
	fnErr := fn()

	// 恢复原来的网络命名空间
	if err := netns.Set(currentNs); err != nil {
		// 如果恢复失败，这是严重错误
		return fmt.Errorf("restore network namespace: %w", err)
	}

	return fnErr
}
