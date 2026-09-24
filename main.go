package main

import (
	"github.com/example/go-frame/mod/user"
	"github.com/example/go-frame/pkg/cli"
	"github.com/example/go-frame/pkg/service/restkit"
	"github.com/spf13/cobra"
)

func main() {
	cli.RootCMD(&cobra.Command{
		Use: "main",
		Run: func(cmd *cobra.Command, args []string) {
			// 权限数据源必须先于路由挂载注册，否则鉴权链路拿不到用户角色/部门
			user.Init()
			restkit.AddActions(user.All()...)
			_ = restkit.Run()
		},
	})
	cli.AddChildCMDWithoutConfig(cli.VersionCMD())
	cli.Execute()
}
