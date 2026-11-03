// lynx-start:bridge —— 由 add 生成（remove 删除）。转发装配入口：
// modules/<name>/internal 受 Go 可见性保护，应用唯一入口是本包。
// 改模块装配请改 internal/api；本文件属注入物，不参与 update 合并。
package access

import (
	"github.com/lynxland/appkit"

	api "github.com/lynxland/appkit-demo/modules/access/internal/api"
)

// Module 转发模块静态装配描述。
func Module() appkit.Module { return api.Module() }

// Assemble 转发模块动态装配。
func Assemble(deps appkit.Deps) (appkit.Assembly, error) { return api.Assemble(deps) }
