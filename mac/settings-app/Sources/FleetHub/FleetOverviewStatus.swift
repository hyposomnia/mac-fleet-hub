import FleetCore

struct FleetOverviewStatus {
    enum Tone { case inactive, online, connecting, warning }
    let title: String
    let tone: Tone

    init(running: Bool, runtime: RuntimeState?, locked: Bool, operationError: String, fallback: String) {
        if !operationError.isEmpty {
            title = operationError
            tone = .warning
        } else if !running {
            title = fallback
            tone = .inactive
        } else if locked {
            title = "设备授权已锁定"
            tone = .warning
        } else {
            switch runtime?.phase {
            case "failed":
                let error = runtime?.error ?? ""
                title = error.isEmpty ? "设备连接失败" : error
                tone = .warning
            case "connecting":
                title = "正在连接设备"
                tone = .connecting
            case "starting":
                title = "正在启动设备服务"
                tone = .connecting
            default:
                title = "后台运行中"
                tone = .online
            }
        }
    }
}
