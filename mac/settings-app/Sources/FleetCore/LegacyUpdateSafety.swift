import Foundation

/// Agent replacement leaves the shared Codex writer alive. Ask the old agent
/// which turns it owns; rollout task markers cannot distinguish Desktop turns.
@MainActor
enum LegacyUpdateSafety {
    static func checkOtherAssistants(listen: String, credentialConfig: Data? = nil,
                                     execute: (URL, [String], Data?, TimeInterval) async throws -> Data) async throws {
        // Claude runs in independent tmux sessions. Replacing the agent and ttyd
        // only detaches their browser transports; never kill those tmux sessions.
        // DSH exposes actual host activity, unlike a historical rollout marker.
        guard let base = URL(string: "http://" + listen), base.host != nil, base.port != nil,
              base.user == nil, base.password == nil, base.query == nil, base.fragment == nil, base.path.isEmpty else {
            throw FleetError.message("无法确认旧 Fleet 的监听地址，尚未停止旧版本。")
        }
        let options = ["--config", "-", "--fail", "--silent", "--show-error", "--max-time", "5", "--noproxy", "*"]
        let info: [String: Any]
        do {
            let data = try await execute(URL(fileURLWithPath: "/usr/bin/curl"), options + [base.appendingPathComponent("api/info").absoluteString], credentialConfig ?? Data(), 8)
            guard let decoded = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw FleetError.message("invalid info") }
            info = decoded
        } catch { throw FleetError.message("暂时无法确认旧 Fleet 的运行状态，尚未停止旧版本，请稍后重试。") }
        // Older agents without DSH, or machines where its host is not running,
        // have no DSH task for this agent replacement to interrupt.
        guard let dsh = info["dsh"] as? [String: Any], dsh["enabled"] as? Bool == true, dsh["hostRunning"] as? Bool == true else { return }
        let sessions: [[String: Any]]
        do {
            let data = try await execute(URL(fileURLWithPath: "/usr/bin/curl"), options + ["--get", "--data-urlencode", "assistant=dsh", base.appendingPathComponent("api/sessions").absoluteString], credentialConfig ?? Data(), 8)
            guard let decoded = try JSONSerialization.jsonObject(with: data) as? [String: Any], let list = decoded["sessions"] as? [[String: Any]] else { throw FleetError.message("invalid sessions") }
            sessions = list
        } catch { throw FleetError.message("暂时无法确认 DeepSeek 的任务状态，尚未停止旧版本，请稍后重试。") }
        guard !sessions.contains(where: { $0["status"] as? String == "active" }) else {
            throw FleetError.message("DeepSeek 会话正在运行，请任务完成后再安装。旧版本和消息均已保留。")
        }
    }

    static func check(codexHome: URL, listen: String, credentialConfig: Data? = nil,
                      execute: (URL, [String], Data?, TimeInterval) async throws -> Data) async throws {
        let locks = codexHome.appendingPathComponent("thread-writer-locks")
        guard FileManager.default.fileExists(atPath: locks.path) else { return }
        for lock in try FileManager.default.contentsOfDirectory(at: locks, includingPropertiesForKeys: nil) where lock.pathExtension == "lock" {
            guard let holders = try? await execute(URL(fileURLWithPath: "/usr/sbin/lsof"), ["-t", "--", lock.path], nil, 8), !holders.isEmpty else { continue }
            let session = lock.deletingPathExtension().lastPathComponent
            guard let url = URL(string: "http://" + listen + "/api/chat/queue"), url.host != nil, url.port != nil, url.user == nil, url.password == nil, url.query == nil, url.fragment == nil else {
                throw FleetError.message("无法确认旧 Fleet 的监听地址，尚未停止旧版本。请在旧 Fleet 恢复运行后重试。")
            }
            let data: Data
            do {
                data = try await execute(URL(fileURLWithPath: "/usr/bin/curl"), ["--config", "-", "--fail", "--silent", "--show-error", "--max-time", "5", "--noproxy", "*", "--get", "--data-urlencode", "assistant=codex", "--data-urlencode", "sessionId=" + session, url.absoluteString], credentialConfig ?? Data(), 8)
            } catch {
                throw FleetError.message("暂时无法确认旧 Fleet 的任务状态，尚未停止旧版本。请恢复旧 Fleet 后重试；无需结束 Desktop 中的会话。")
            }
            guard let snapshot = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let items = snapshot["items"] as? [[String: Any]],
                  let phase = snapshot["turnPhase"] as? String else {
                throw FleetError.message("旧 Fleet 未返回可识别的任务状态，尚未停止旧版本。请等待旧 Fleet 任务完成后重试。")
            }
            let owner = snapshot["turnOwner"] as? String ?? ""
            let inFlight = Set(["sending", "steering", "taking_over", "recovering", "takeover_check"])
            guard !items.contains(where: { inFlight.contains($0["status"] as? String ?? "") }),
                  (phase == "idle" && owner != "fleet") || (phase == "running" && owner == "desktop") else {
                throw FleetError.message("旧 Fleet 正在执行任务或投递消息，请完成后再安装。Desktop 会话可以继续运行；旧版本和消息均已保留。")
            }
        }
    }
}
