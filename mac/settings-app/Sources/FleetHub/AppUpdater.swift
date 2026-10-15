import AppKit
import Darwin
import FleetCore
import Sparkle

@MainActor
final class AppUpdater: NSObject, ObservableObject, SPUUpdaterDelegate {
    @Published private(set) var message = ""
    @Published private(set) var busy = false
    @Published private(set) var sessionActive = false
    @Published private(set) var waitingForIdle = false
    @Published private(set) var installationNeedsAttention = false
    private var maintenanceWait: MaintenanceWait?
    @Published private(set) var availableVersion: String?
    private let management: NativeManagement
    private var controller: SPUStandardUpdaterController?
    private var release: NativeRelease?
    private var origin = ""
    private var preparation = UpdatePreparation()
    private var installing = false
    private var recovered = false
    private var installHandler: (() -> Void)?
    private var recovery: Recovery?

    struct Recovery: Codable {
        var directory: UUID
        var previousBuild: Int64
        var expectedBuild: Int64
        var previousPID: Int

        func accepts(installedBuild: Int64) -> Bool {
            expectedBuild > previousBuild && (installedBuild == previousBuild || installedBuild >= expectedBuild)
        }
    }

    init(management: NativeManagement) { self.management = management }
    private var currentBuild: Int64 { Int64(Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "") ?? 0 }
    private var currentVersion: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "" }
    private var journal: URL { management.layout.state.appendingPathComponent("pending-update.json") }
    var recoveryPending: Bool { FileManager.default.fileExists(atPath: journal.path) }
    private func backupURL(_ record: Recovery) -> URL {
        management.layout.state.appendingPathComponent("updates/\(record.directory.uuidString)/Fleet Hub.app")
    }

    private var hasUpdateSigningKey: Bool {
        guard let key = Bundle.main.object(forInfoDictionaryKey: "SUPublicEDKey") as? String else { return false }
        return Data(base64Encoded: key)?.count == 32
    }

    private func validatedCandidate() async throws -> (origin: String, release: NativeRelease) {
        let candidateOrigin = try await management.updateOrigin()
        let candidate = try await NativeRelease.fetch(origin: candidateOrigin)
        #if arch(arm64)
        let architecture = "arm64"
        #else
        let architecture = "x86_64"
        #endif
        let system = ProcessInfo.processInfo.operatingSystemVersion
        try candidate.validate(currentBuild: currentBuild, architecture: architecture,
                               system: "\(system.majorVersion).\(system.minorVersion).\(system.patchVersion)")
        return (candidateOrigin, candidate)
    }

    func checkAvailability() async {
        availableVersion = nil
        guard !management.layout.requiresInstallation, !recoveryPending, hasUpdateSigningKey else { return }
        do {
            let candidate = try await validatedCandidate().release
            if candidate.build > currentBuild { availableVersion = candidate.version }
        } catch {
            // Passive discovery stays quiet when the device or update server is unavailable.
        }
    }

    func check() async {
        guard !busy, !sessionActive, !waitingForIdle, controller?.updater.sessionInProgress != true else { return }
        busy = true
        defer { busy = false }
        do {
            guard !management.layout.requiresInstallation else { throw FleetError.message("请先安装到应用程序，再检查升级。") }
            guard hasUpdateSigningKey else { throw FleetError.message("当前是开发构建，未配置正式升级签名公钥，不执行安装。") }
            guard !FileManager.default.fileExists(atPath: journal.path) else { throw FleetError.message("上次升级尚未完成健康验证，请先重新打开应用。") }
            let candidateInfo = try await validatedCandidate()
            origin = candidateInfo.origin
            let candidate = candidateInfo.release
            guard candidate.build > currentBuild else {
                availableVersion = nil
                message = "当前已是该服务器提供的最新版本。"
                return
            }
            availableVersion = candidate.version
            release = candidate
            preparation = UpdatePreparation()
            installationNeedsAttention = false
            let updater = SPUStandardUpdaterController(startingUpdater: false, updaterDelegate: self, userDriverDelegate: nil)
            controller = updater
            _ = updater.updater.clearFeedURLFromUserDefaults()
            updater.updater.automaticallyChecksForUpdates = false
            updater.updater.automaticallyDownloadsUpdates = false
            try updater.updater.start()
            sessionActive = true
            updater.checkForUpdates(nil)
            message = "已找到 \(candidate.version)，升级会替换整个应用，保留设备关联与设置。"
        } catch { message = error.localizedDescription }
    }

    func feedURLString(for updater: SPUUpdater) -> String? { origin.isEmpty ? nil : origin + "/enroll/appcast.xml" }
    func updater(_ updater: SPUUpdater, shouldDownloadReleaseNotesForUpdate item: SUAppcastItem) -> Bool { false }
    func updater(_ updater: SPUUpdater, shouldProceedWithUpdate item: SUAppcastItem, updateCheck: SPUUpdateCheck) throws {
        let enclosure = item.propertiesDictionary["enclosure"] as? [String: Any]
        guard let release, item.versionString == String(release.build),
              enclosure?["sparkle:edSignature"] as? String == release.assets.update.edSignature,
              item.fileURL?.absoluteString == origin + release.assets.update.path,
              item.contentLength == UInt64(release.assets.update.size), !item.isDeltaUpdate else {
            throw FleetError.message("升级源与已校验的同源发行清单不一致，已拒绝安装。")
        }
    }
    func updater(_ updater: SPUUpdater, willDownloadUpdate item: SUAppcastItem, with request: NSMutableURLRequest) {
        message = "正在下载升级包…"
        request.httpShouldHandleCookies = false
        request.setValue(nil, forHTTPHeaderField: "Cookie")
        request.setValue(nil, forHTTPHeaderField: "Authorization")
    }
    func updater(_ updater: SPUUpdater, willExtractUpdate item: SUAppcastItem) { message = "正在解压升级包…" }
    func updater(_ updater: SPUUpdater, didExtractUpdate item: SUAppcastItem) { message = "升级包已准备好，请在升级窗口继续安装。" }
    func updater(_ updater: SPUUpdater, willInstallUpdate item: SUAppcastItem) {
        installing = true
        message = "正在准备安装升级…"
    }
    func updater(_ updater: SPUUpdater, shouldPostponeRelaunchForUpdate item: SUAppcastItem, untilInvokingBlock handler: @escaping () -> Void) -> Bool {
        installing = true
        installHandler = handler
        installationNeedsAttention = false
        Task { await continueInstallation() }
        return true
    }
    func updater(_ updater: SPUUpdater, didAbortWithError error: Error) {
        maintenanceWait?.cancel()
        message = error.localizedDescription
        installing = false
        installHandler = nil
        installationNeedsAttention = false
        preparation.cancel()
        if !busy { Task { await recoverAfterLaunch(force: true) } }
    }
    func updater(_ updater: SPUUpdater, didFinishUpdateCycleFor updateCheck: SPUUpdateCheck, error: Error?) {
        maintenanceWait?.cancel()
        sessionActive = false
        installing = false
        installHandler = nil
        installationNeedsAttention = false
        preparation.cancel()
        if let error { message = error.localizedDescription }
    }

    func waitForIdleAndInstall() async {
        guard installationNeedsAttention, installHandler != nil, !busy, !waitingForIdle else { return }
        let wait = MaintenanceWait()
        maintenanceWait = wait
        waitingForIdle = true
        installationNeedsAttention = false
        message = "正在等待后台任务结束，结束后自动安装。"
        defer {
            maintenanceWait = nil
            waitingForIdle = false
            installationNeedsAttention = installHandler != nil && !preparation.isCancelled
        }
        do {
            try await wait.run(check: {
                do {
                    try await self.management.prepareForStop()
                    await self.management.resumeAfterCancelledStop()
                } catch {
                    if self.installHandler != nil { self.message = "等待任务结束后安装：" + error.localizedDescription }
                    throw error
                }
            }, action: { await self.installPendingUpdate() })
        } catch is CancellationError {
            if installHandler != nil { message = "已取消等待，后台任务继续运行。" }
        } catch {
            if installHandler != nil { message = error.localizedDescription }
        }
    }

    func cancelWaitingForIdle() {
        maintenanceWait?.cancel()
        if waitingForIdle { message = "正在取消等待…" }
    }

    func continueInstallation() async {
        guard !waitingForIdle else { return }
        await installPendingUpdate()
    }

    private func installPendingUpdate() async {
        guard let handler = installHandler, !busy else { return }
        installationNeedsAttention = false
        message = "正在检查后台任务并准备升级…"
        do {
            try await prepare()
            installHandler = nil
            message = "正在安装升级并重新打开应用…"
            handler()
        }
        catch {
            if installHandler != nil {
                message = "升级尚未安装：" + error.localizedDescription
                installationNeedsAttention = !preparation.isCancelled && !waitingForIdle
            }
            if preparation.isCancelled { await recoverAfterLaunch(force: true) }
        }
    }

    func shouldTerminate() -> NSApplication.TerminateReply {
        guard installing, !preparation.isPrepared else { return .terminateNow }
        Task {
            do { try await prepare(); NSApplication.shared.reply(toApplicationShouldTerminate: true) }
            catch {
                message = error.localizedDescription
                NSApplication.shared.reply(toApplicationShouldTerminate: false)
                if preparation.isCancelled { await recoverAfterLaunch(force: true) }
            }
        }
        return .terminateLater
    }

    private func prepare() async throws {
        guard let release else { throw FleetError.message("缺少已校验的升级清单。") }
        busy = true
        defer { busy = false }
        try await preparation.prepare(check: {
            try await self.management.prepareForStop()
            await self.management.resumeAfterCancelledStop()
        }, backup: {
            if self.recovery != nil { return }
            let status = try? await self.management.status()
            let record = Recovery(directory: UUID(), previousBuild: self.currentBuild, expectedBuild: release.build, previousPID: status?.pid ?? 0)
            let backup = self.backupURL(record)
            try PrivateRuntime.ensureDirectory(backup.deletingLastPathComponent())
            try await ApplicationInstaller.install(source: self.management.layout.application, destination: backup, verify: self.verify)
            try PrivateRuntime.write(JSONEncoder().encode(record), to: self.journal)
            self.recovery = record
        }, stop: { try await self.management.stop() }, resume: {
            if (try? await self.management.status()) == nil { try? await self.management.start() }
        })
    }

    private func verify(_ application: URL) async throws {
        guard Bundle(url: application)?.bundleIdentifier == "com.macfleet.fleet-hub" else { throw FleetError.message("应用身份无效。") }
        _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/bin/codesign"), arguments: ["--verify", "--deep", "--strict", application.path], timeout: 30)
        _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/sbin/spctl"), arguments: ["--assess", "--type", "execute", application.path], timeout: 30)
    }

    func recoverAfterLaunch(force: Bool = false) async {
        guard !management.layout.requiresInstallation else { return }
        guard !recovered || force else { return }
        recovered = true
        guard FileManager.default.fileExists(atPath: journal.path) else { return }
        busy = true
        defer { busy = false }
        do {
            let record = try JSONDecoder().decode(Recovery.self, from: PrivateRuntime.read(journal))
            guard record.accepts(installedBuild: currentBuild) else {
                throw FleetError.message("升级恢复记录与当前版本不一致，未修改应用。")
            }
            let installedUpgrade = currentBuild >= record.expectedBuild
            try? await management.start()
            for _ in 0..<150 {
                if let status = try? await management.status(),
                   status.isHealthyAfterUpdate(version: currentVersion, build: currentBuild,
                                               replacingPID: installedUpgrade ? record.previousPID : nil) {
                    try FileManager.default.removeItem(at: journal)
                    recovery = nil
                    preparation = UpdatePreparation()
                    installing = false
                    installHandler = nil
                    installationNeedsAttention = false
                    message = installedUpgrade ?
                        (status.binding?.complete == false ? "升级完成，请重新完成设备关联。" : (status.runtime?.phase == "connecting" || status.runtime?.phase == "reconnecting" ? "升级完成，正在重新连接。" : "升级完成。")) :
                        "已恢复原版本后台；升级未完成。"
                    return
                }
                try await Task.sleep(nanoseconds: 200_000_000)
            }
            guard installedUpgrade else { throw FleetError.message("原版本后台未通过健康检查，已保留恢复记录。") }
            if (try? await management.status()) != nil { try await management.stop() }
            let backup = backupURL(record)
            try await verify(backup)
            guard renamex_np(backup.path, management.layout.application.path, UInt32(RENAME_SWAP)) == 0 else { throw FleetError.message("升级健康检查失败，自动回滚未成功；备份与恢复记录已保留。") }
            _ = try await CommandRunner.run(executable: URL(fileURLWithPath: "/usr/bin/open"), arguments: ["-n", management.layout.application.path])
            installing = false
            NSApplication.shared.terminate(nil)
        } catch { message = error.localizedDescription }
    }
}

@MainActor
final class FleetApplicationDelegate: NSObject, NSApplicationDelegate {
    weak var updater: AppUpdater?
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply { updater?.shouldTerminate() ?? .terminateNow }
}
