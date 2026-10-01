# 源码快照占位

本目录是 `go:embed all:_snapshot` 的挂载点(下划线前缀让 go 工具链忽略此
目录,不参与编译与 ./... 遍历),内容由 `scripts/build-standalone.mjs` 在
构建前同步(backend 的 Go 源码与预置协议 JSON、webui 的 src),随二进制
分发给内置助手的 `elysia code` 命令查看。除本占位文件外的内容不进 git;
纯 `go build` 的开发构建里快照为空,命令会返回相应提示。
