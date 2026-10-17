import SwiftUI
import WebKit
import CoreImage.CIFilterBuiltins

enum NativeAuthStage: String {
    case login, register, recovery, challenge, setup, codes
}

struct NativeAuthFailure: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

@MainActor
final class NativeAuthentication: ObservableObject, Identifiable {
    typealias Transport = (String, String, [String: String]?) async throws -> [String: Any]
    let id = UUID()
    @Published private(set) var stage: NativeAuthStage = .login
    @Published private(set) var busy = false
    @Published private(set) var transportReady = false
    @Published private(set) var secret = ""
    @Published private(set) var totpURI = ""
    @Published private(set) var codes: [String] = []
    @Published var error: String?
    @Published var email = ""
    @Published var password = ""
    @Published var confirmation = ""
    @Published var recoveryCode = ""
    @Published var code = ""
    private let transport: Transport
    private let completed: () -> Void
    private var active = true

    init(transport: @escaping Transport, completed: @escaping () -> Void) {
        self.transport = transport
        self.completed = completed
    }

    static func isAuthRoute(_ url: URL) -> Bool { url.path == "/auth" || url.path.hasPrefix("/auth/") }
    static func allowed(_ path: String, method: String) -> Bool {
        method == "GET" ? path == "/api/auth/state" :
            method == "POST" && ["/api/auth/login", "/api/auth/register", "/api/auth/recover", "/api/auth/verify"].contains(path)
    }

    func invalidate() {
        active = false
        password = ""; confirmation = ""; recoveryCode = ""; code = ""
        secret = ""; totpURI = ""; codes = []; email = ""
    }

    func select(_ value: NativeAuthStage) {
        guard !busy, [.login, .register, .recovery].contains(value), stage != .codes else { return }
        stage = value; error = nil
        password = ""; confirmation = ""; recoveryCode = ""; code = ""
        secret = ""; totpURI = ""
    }

    func resume() async {
        guard active, !busy, stage != .codes else { return }
        busy = true; error = nil
        defer { busy = false }
        do {
            let result = try await transport("/api/auth/state", "GET", nil)
            guard active else { return }
            transportReady = true
            if result["state"] as? String == "full" { finish() }
            else if result["state"] as? String == "anonymous" { stage = .login }
            else { try transition(result) }
        } catch {
            if active { self.error = "无法连接登录服务，请重试。" }
        }
    }

    func submit() async {
        guard active, transportReady, !busy, stage != .codes else { return }
        let path: String
        let body: [String: String]
        error = nil
        switch stage {
        case .login, .register, .recovery:
            let address = email.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !address.isEmpty, !password.isEmpty else { error = "请输入邮箱和密码。"; return }
            if stage == .register {
                guard password.count >= 12, password.utf8.count <= 256 else { error = "密码至少 12 个字符，且不超过 256 字节。"; return }
                guard password == confirmation else { error = "两次输入的密码不一致。"; return }
            }
            if stage == .recovery && recoveryCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                error = "请输入一个未使用的恢复码。"; return
            }
            path = stage == .register ? "/api/auth/register" : stage == .recovery ? "/api/auth/recover" : "/api/auth/login"
            body = ["email": address, "password": password, "confirm_password": confirmation, "recovery_code": recoveryCode.trimmingCharacters(in: .whitespacesAndNewlines)]
        case .challenge, .setup:
            guard code.count == 6, code.utf8.allSatisfy({ (48...57).contains($0) }) else { error = "请输入六位验证码。"; return }
            path = "/api/auth/verify"; body = ["code": code]
        case .codes: return
        }
        busy = true
        defer { busy = false }
        do {
            let result = try await transport(path, "POST", body)
            guard active else { return }
            password = ""; confirmation = ""; recoveryCode = ""; code = ""
            if path == "/api/auth/verify" {
                guard result["user"] is [String: Any] else { throw NativeAuthFailure(message: "验证响应无效，请重新登录。") }
                secret = ""; totpURI = ""
                if let values = result["recovery_codes"] as? [String], !values.isEmpty {
                    codes = values; stage = .codes
                } else { finish() }
            } else { try transition(result) }
        } catch {
            if active { self.error = (error as? NativeAuthFailure)?.message ?? "连接中断，请检查网络后重试。" }
        }
    }

    private func transition(_ result: [String: Any]) throws {
        switch result["state"] as? String {
        case "challenge": stage = .challenge
        case "setup":
            guard let totp = result["totp"] as? [String: String], let secret = totp["secret"], !secret.isEmpty,
                  let uri = totp["uri"], uri.hasPrefix("otpauth://totp/") else {
                throw NativeAuthFailure(message: "验证器设置响应无效，请重新登录。")
            }
            self.secret = secret; totpURI = uri; stage = .setup
        default: throw NativeAuthFailure(message: "登录流程响应无效，请重新登录。")
        }
    }

    func finish() {
        guard active else { return }
        invalidate()
        completed()
    }
}

extension FleetStore {
    func beginAuthentication() {
        guard authentication == nil else { return }
        reset()
        let origin = gateway
        authentication = NativeAuthentication(transport: { [weak self] path, method, body in
            guard let self, let origin, self.gateway == origin, let model = self.authentication,
                  let web = self.webView, let url = web.url,
                  GatewayAddress.sameOrigin(url, origin), NativeAuthentication.isAuthRoute(url),
                  NativeAuthentication.allowed(path, method: method) else {
                throw NativeAuthFailure(message: "登录连接已变化，请重新连接。")
            }
            // Cookie stays in WebKit. Only native controls collect credentials; no DOM form is used.
            let result = try await web.callAsyncJavaScript("""
                if (location.origin !== origin || (location.pathname !== '/auth' && !location.pathname.startsWith('/auth/'))) throw new Error('Invalid authentication origin');
                const controller = new AbortController();
                const timer = setTimeout(() => controller.abort(), 25000);
                try {
                    const response = await fetch(path, {method, credentials:'same-origin', cache:'no-store', redirect:'error',
                        headers: {'Accept':'application/json', ...(body ? {'Content-Type':'application/json'} : {})},
                        ...(body ? {body:JSON.stringify(body)} : {}), signal:controller.signal});
                    const payload = await response.json();
                    return {status:response.status, payload};
                } finally { clearTimeout(timer); }
                """, arguments: ["origin": origin.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/")), "path": path, "method": method, "body": (body as Any?) ?? NSNull()], in: nil, contentWorld: .defaultClient)
            guard self.authentication === model, self.gateway == origin,
                  let currentURL = web.url, GatewayAddress.sameOrigin(currentURL, origin), NativeAuthentication.isAuthRoute(currentURL),
                  let response = result as? [String: Any], let status = response["status"] as? Int,
                  let payload = response["payload"] as? [String: Any] else {
                throw NativeAuthFailure(message: "登录连接已变化，请重新连接。")
            }
            if status == 401 && path == "/api/auth/state" { return ["state": "anonymous"] }
            guard (200..<300).contains(status) else {
                throw NativeAuthFailure(message: payload["message"] as? String ?? payload["error"] as? String ?? "登录未完成，请重试。")
            }
            return payload
        }, completed: { [weak self] in
            guard let self, self.gateway == origin, let origin else { return }
            self.authentication = nil
            self.loading = true
            self.webView?.load(URLRequest(url: origin))
        })
    }
}

struct NativeAuthenticationView: View {
    @Environment(\.fleetStyle) private var style
    @ObservedObject var store: FleetStore
    @ObservedObject var model: NativeAuthentication
    @FocusState private var focused: Field?
    @State private var savedCodes = false
    @State private var copied = false
    private enum Field: Hashable { case email, password, confirmation, recovery, code }

    private var title: String {
        switch model.stage {
        case .login: return "登录 Fleet"
        case .register: return "创建账号"
        case .recovery: return "恢复账号"
        case .challenge: return "两步验证"
        case .setup: return "设置验证器"
        case .codes: return "保存恢复码"
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image("FleetMark").resizable().scaledToFit().frame(width: 26, height: 26).foregroundStyle(style.color("accent"))
                Text("FLEET HUB").font(.system(size: 14, weight: .semibold, design: .monospaced)).tracking(1)
                Spacer()
                if model.stage != .codes && model.stage != .setup {
                    Button { focused = nil; store.nativeSheet = .gateway } label: { Image(systemName: "network") }
                        .buttonStyle(TitaniumIconButtonStyle()).accessibilityLabel("网关设置").disabled(model.busy)
                }
            }.padding(.horizontal, 24).padding(.vertical, 8)
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    VStack(alignment: .leading, spacing: 10) {
                        Text(title).font(.system(size: 30, weight: .bold)).accessibilityAddTraits(.isHeader)
                        Text(subtitle).font(.system(size: 15)).foregroundStyle(style.color("text-2"))
                    }
                    if model.stage == .codes { recoveryCodes }
                    else {
                        if model.stage == .setup { authenticatorSetup }
                        VStack(spacing: 18) {
                            if [.login, .register, .recovery].contains(model.stage) {
                                field("邮箱", value: $model.email, kind: .email)
                                field(model.stage == .register ? "密码（至少 12 个字符）" : "密码", value: $model.password, kind: .password)
                                if model.stage == .register { field("确认密码", value: $model.confirmation, kind: .confirmation) }
                                if model.stage == .recovery { field("恢复码", value: $model.recoveryCode, kind: .recovery) }
                            } else { field("六位验证码", value: $model.code, kind: .code) }
                        }.disabled(model.busy)
                        if let error = model.error {
                            Text(error).font(.system(size: 14)).foregroundStyle(.red).accessibilityIdentifier("native-auth-error")
                        }
                        Button { submit() } label: {
                            HStack(spacing: 10) {
                                if model.busy { ProgressView().tint(style.color("accent-contrast")) }
                                Text(model.stage == .register ? "注册" : model.stage == .setup ? "验证并完成设置" : model.stage == .challenge ? "登录" : model.stage == .recovery ? "恢复并重新设置" : "继续")
                            }.frame(maxWidth: .infinity, minHeight: 8)
                        }.buttonStyle(NativePrimaryButtonStyle()).disabled(model.busy || !model.transportReady)
                            .accessibilityIdentifier("native-auth-submit")
                        if !model.transportReady && !model.busy {
                            Button("重新连接") { store.webView?.reload() }.buttonStyle(NativeTextButtonStyle())
                        }
                        authLinks
                    }
                    Text(store.gateway?.host ?? "").font(.system(size: 12)).foregroundStyle(style.color("text-2"))
                        .frame(maxWidth: .infinity).padding(.top, 4)
                }.frame(maxWidth: 440).padding(.horizontal, 24).padding(.top, 36).padding(.bottom, 24)
                    .frame(maxWidth: .infinity)
            }.scrollDismissesKeyboard(.interactively)
        }
        .background(style.color("bg").ignoresSafeArea())
        .accessibilityElement(children: .contain).accessibilityIdentifier("native-authentication")
        .onChange(of: model.stage) { _, _ in focused = nil; savedCodes = false; copied = false }
    }

    private var subtitle: String {
        switch model.stage {
        case .login: return "连接你的 AI 工作区"
        case .register: return "注册后绑定验证器，保护你的工作区。"
        case .recovery: return "输入密码和未使用的恢复码，重新绑定验证器。"
        case .challenge: return "输入验证器 App 中的六位验证码。"
        case .setup: return "用验证器 App 扫码，或手动输入设置密钥。"
        case .codes: return "每个恢复码只能使用一次。离开后无法再次查看，请妥善保存。"
        }
    }

    private func field(_ title: String, value: Binding<String>, kind: Field) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            NativeFieldLabel(title: title, focused: focused == kind) { focused = nil }
            Group {
                if kind == .password || kind == .confirmation {
                    SecureField(title, text: value)
                        .textContentType(model.stage == .register ? .newPassword : .password)
                } else {
                    TextField(title, text: value)
                        .keyboardType(kind == .email ? .emailAddress : kind == .code ? .numberPad : .asciiCapable)
                        .textContentType(kind == .email ? .username : kind == .code ? .oneTimeCode : nil)
                }
            }
            .focused($focused, equals: kind).textInputAutocapitalization(.never).autocorrectionDisabled()
            .submitLabel(kind == .email || (kind == .password && model.stage != .login) ? .next : .go)
            .onSubmit {
                if kind == .email { focused = .password }
                else if kind == .password && model.stage == .register { focused = .confirmation }
                else if kind == .password && model.stage == .recovery { focused = .recovery }
                else { submit() }
            }
            .font(.system(size: 17)).padding(.horizontal, 14).frame(minHeight: 52)
            .background(style.color("surface-2"), in: RoundedRectangle(cornerRadius: 12))
            .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(focused == kind ? style.color("accent") : .clear, lineWidth: 1.5))
            .accessibilityIdentifier("native-auth-\(kind)")
        }
    }

    @ViewBuilder private var authLinks: some View {
        if model.stage == .login {
            HStack {
                Button("注册账号") { model.select(.register) }.accessibilityIdentifier("native-auth-register")
                Spacer()
                Button("使用恢复码") { model.select(.recovery) }.accessibilityIdentifier("native-auth-recovery")
            }.buttonStyle(NativeTextButtonStyle()).foregroundStyle(style.color("accent-text")).disabled(model.busy)
        } else if model.stage != .codes {
            Button("返回登录") { model.select(.login) }.buttonStyle(NativeTextButtonStyle()).foregroundStyle(style.color("accent-text")).disabled(model.busy)
        }
    }

    private var authenticatorSetup: some View {
        VStack(spacing: 16) {
            if let image = qrImage {
                Image(uiImage: image).interpolation(.none).resizable().scaledToFit().frame(width: 184, height: 184)
                    .padding(12).background(.white, in: RoundedRectangle(cornerRadius: 12)).accessibilityLabel("验证器设置二维码")
            }
            Text(model.secret).font(.system(size: 16, weight: .medium, design: .monospaced)).textSelection(.enabled)
                .accessibilityIdentifier("native-auth-secret")
            Button("复制设置密钥") { FleetService.copy(model.secret, sensitive: true) }.buttonStyle(NativeTextButtonStyle())
        }.frame(maxWidth: .infinity)
    }

    private var recoveryCodes: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text(model.codes.joined(separator: "\n")).font(.system(size: 16, weight: .medium, design: .monospaced))
                .textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading).padding(18)
                .background(style.color("surface-2"), in: RoundedRectangle(cornerRadius: 12))
            Button(copied ? "已复制" : "复制恢复码") { FleetService.copy(model.codes.joined(separator: "\n"), sensitive: true); copied = true }
                .buttonStyle(NativeTextButtonStyle())
            Toggle("我已安全保存恢复码", isOn: $savedCodes).accessibilityIdentifier("native-auth-saved-codes")
            Button { model.finish() } label: { Text("进入工作区").frame(maxWidth: .infinity, minHeight: 8) }
                .buttonStyle(NativePrimaryButtonStyle()).disabled(!savedCodes).accessibilityIdentifier("native-auth-finish")
        }
    }

    private var qrImage: UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(model.totpURI.utf8)
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)),
              let image = CIContext().createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: image)
    }

    private func submit() { focused = nil; Task { await model.submit() } }
}
