// Package modules 登记应用已安装的 lynx-start 模块。
// 本文件由 lynx-start add 维护（安装清单见仓库根 lynx-start.json）；锚点注释勿删。
package modules

import (
	"github.com/lynxland/appkit"
	access "github.com/lynxland/appkit-demo/modules/access"
	audit "github.com/lynxland/appkit-demo/modules/audit"
	demo "github.com/lynxland/appkit-demo/modules/demo"
	identity "github.com/lynxland/appkit-demo/modules/identity"
	notification "github.com/lynxland/appkit-demo/modules/notification"
	organization "github.com/lynxland/appkit-demo/modules/organization"
	session "github.com/lynxland/appkit-demo/modules/session"
	task "github.com/lynxland/appkit-demo/modules/task"
	// lynx-start:module-import（lynx-start add 注入锚点，勿删）
)

// Installed 返回全部已装模块（静态描述 + 动态装配）。
func Installed() []appkit.InstalledModule {
	return []appkit.InstalledModule{
		{
			Module:   demo.Module(),
			Assemble: demo.Assemble,
		},
		{
			Module:   identity.Module(),
			Assemble: identity.Assemble,
		},
		{
			Module:   session.Module(),
			Assemble: session.Assemble,
		},
		{
			Module:   access.Module(),
			Assemble: access.Assemble,
		},
		{
			Module:   audit.Module(),
			Assemble: audit.Assemble,
		},
		{
			Module:   organization.Module(),
			Assemble: organization.Assemble,
		},
		{
			Module:   notification.Module(),
			Assemble: notification.Assemble,
		},
		{
			Module:   task.Module(),
			Assemble: task.Assemble,
		},
		// lynx-start:module-entry（lynx-start add 注入锚点，勿删）
	}
}
