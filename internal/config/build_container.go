//go:build agentdock_docker

package config

// ContainerBuild 固定官方 Docker 镜像的能力边界，不从可覆盖的环境变量推断。
const ContainerBuild = true
