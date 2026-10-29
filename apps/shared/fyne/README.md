# 共享移动客户端

独立 Go module（保留 `github.com/rectcircle/fn-connect-private-network/mobile` module path），Android 和 iOS/iPadOS 共用 Fyne UI、连接接口与 Go clienthost 核心。
Android 使用 Java/JNI VpnService；iOS 使用原生桥与 Swift Packet Tunnel 扩展。
系统适配不依赖 Fyne，运行模型见 [架构](../../../docs/architecture.md#平台运行模型)。

- [Android 构建、签名与验证](../../../docs/development.md#android-开发与构建)
- [iOS 环境与构建](../../../docs/development.md#ios-开发环境准备)
- iOS 安装：[中文](../../../docs/ios-installation.zh-CN.md) / [English](../../../docs/ios-installation.en.md)
- [平台范围](../../../docs/platform-support.md)与[发布验收](../../../docs/validation.md)

```sh
go test -tags ci ./...
go test -race -tags ci ./...
go vet -tags ci ./...
```

iOS 模拟器内置真实共享核心和应用内网络调试，仅显式 HTTP/DNS 请求进入用户态隧道。
系统 Packet Tunnel 与其他 App 的网络流量仍需真机验证。
