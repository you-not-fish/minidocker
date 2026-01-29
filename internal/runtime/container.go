package runtime

import (
	"minidocker/internal/cgroups"
	"minidocker/pkg/idutil"
)

// ContainerConfig 保存容器的配置。
// 该结构体设计为可扩展以适应未来的阶段。
type ContainerConfig struct {
	// ID 是容器的唯一标识符（64个字符的十六进制）。
	// 前12个字符用作默认主机名（Docker 惯例）。
	ID string

	// Command 是在容器中运行的主要命令。
	Command []string

	// Args 是传递给命令的附加参数。
	Args []string

	// Hostname 是在容器内设置的主机名。
	// 默认为容器 ID 的前12个字符。
	Hostname string

	// TTY 指示是否分配伪终端。
	// 第一阶段提供基本支持；完整的 PTY 处理在第五阶段。
	TTY bool

	// --- 未来阶段的字段（定义但未在第一阶段实现） ---

	// Env 保存环境变量（第11阶段：-e KEY=VALUE）
	Env []string

	// WorkingDir 是容器内的工作目录（第11阶段：--workdir）
	WorkingDir string

	// Rootfs 是根文件系统的路径（第2阶段：--rootfs）
	Rootfs string

	// Detached 指示容器是否后台运行（第3阶段：-d）
	Detached bool

	// User 指定在容器内运行的用户（第11阶段：--user）
	User string

	// --- Phase 11 预留字段（当前不实现） ---
	// Name 是容器名称，用于替代 ID 进行引用
	// 在 Phase 11 实现完整的名称到 ID 映射功能
	// Name string

	// --- Phase 6: cgroup 资源限制 ---
	// CgroupConfig 保存容器的资源限制配置（内存、CPU、进程数）
	CgroupConfig *cgroups.CgroupConfig

	// --- 用于未来扩展的占位符字段 ---
	// 这些被注释掉是为了避免循环导入和未使用的代码警告。
	// 在后续阶段根据需要取消注释并实现。
	//
	// NetworkConfig *NetworkConfig // 第7阶段：网络配置
	// Mounts        []Mount        // 第10阶段：卷挂载
}

// GenerateContainerID 生成一个随机的64个字符的十六进制字符串。
// 这遵循 Docker 的容器 ID 惯例。
func GenerateContainerID() string {
	return idutil.GenerateID()
}

// ShortID 返回容器 ID 的前12个字符。
// 这是 Docker 使用的标准"短 ID"格式。
func (c *ContainerConfig) ShortID() string {
	return idutil.ShortID(c.ID)
}

// GetHostname 返回容器的主机名。
// 如果未显式设置，则默认为短 ID。
func (c *ContainerConfig) GetHostname() string {
	if c.Hostname != "" {
		return c.Hostname
	}
	return c.ShortID()
}

// GetCommand 以单个切片形式返回完整命令（命令 + 参数）。
func (c *ContainerConfig) GetCommand() []string {
	cmd := make([]string, 0, len(c.Command)+len(c.Args))
	cmd = append(cmd, c.Command...)
	cmd = append(cmd, c.Args...)
	return cmd
}
