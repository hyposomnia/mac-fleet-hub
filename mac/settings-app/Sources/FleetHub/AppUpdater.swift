import AppKit
import Darwin
import FleetCore
import Sparkle

@MainActor
final class AppUpdater: NSObject, ObservableObject, SPUUpdaterDelegate {
    @Published private(set) var message = ""
    @Published private(set) var busy = false
    @Published private(set) var sessionActive = false
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

    func check() async {
        guard !busy, controller?.updater.sessionInProgress != true else { return }
        busy = true
        defer { busy = false }
        do {
            guard !management.layout.requiresInstallation else { throw FleetError.message("请先安装到应用程序，再检查升级。") }
            guard let key = Bundle.main.object(forInfoDictionaryKey: "SUPublicEDKey") as? String,
                  Data(base64Encoded: key)?.count == 32 else { throw FleetError.message("当前是开发构建，未配置正式升级签名公钥，不执行安装。") }
            guard !FileManager.default.fileExists(atPath: journal.path) else { throw FleetError.message("上次升级尚未完成健康验证，请先重新打开应用。") }
            let status = try await management.status()
            origin = try FleetSettings.validatedOrigin(status.settings.origin)
            let candidate = try await NativeRelease.fetch(origin: origin)
            #if arch(arm64)
            let architecture = "arm64"
            #else
            let architecture = "x86_64"
            #endif
            let system = ProcessInfo.processInfo.operatingSystemVersion
            try candidate.validate(currentBuild: currentBuild, architecture: architecture, system: "\(system.majorVersion).\(system.minorVersion).\(system.patchVersion)")
            guard candidate.build > currentBuild else { message = "当前已是该服务器提供的最新版本。"; return }
            release = candidate
            preparation = UpdatePreparation()
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
        request.httpShouldHandleCookies = false
        request.setValue(nil, forHTTPHeaderField: "Cookie")
        request.setValue(nil, forHTTPHeaderField: "Authorization")
    }
    func updater(_ updater: SPUUpdater, willInstallUpdate item: SUAppcastItem) { installing = true }
    func updater(_ updater: SPUUpdater, shouldPostponeRelaunchForUpdate item: SUAppcastItem, untilInvokingBlock handler: @escaping () -> Void) -> Bool {
        installing = true
        installHandler = handler
        Task { await continueInstallation() }
        return true
    }
    func updater(_ updater: SPUUpdater, didAbortWithError error: Error) {
        message = error.localizedDescription
        installing = false
        installHandler = nil
        preparation.cancel()
        if !busy { Task { await recoverAfterLaunch(force: true) } }
    }
    func updater(_ updater: SPUUpdater, didFinishUpdateCycleFor updateCheck: SPUUpdateCheck, error: Error?) {
        sessionActive = false
    }

    func continueInstallation() async {
        guard installHandler != nil else { return }
        do { try await prepare(); installHandler?(); installHandler = nil }
        catch {
            message = "升级尚未安装：" + error.localizedDescription
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
            let status = try await self.management.status()
            let record = Recovery(directory: UUID(), previousBuild: self.currentBuild, expectedBuild: release.build, previousPID: status.pid)
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
                    message = installedUpgrade ?
                        (status.binding?.complete == false ? "升级完成，请重新完成设备关联。" : "升级完成。") :
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
