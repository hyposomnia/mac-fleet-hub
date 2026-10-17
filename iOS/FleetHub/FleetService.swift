import Foundation
import WebKit
import UIKit

@MainActor
struct FleetService {
    unowned let store: FleetStore

    func request(_ path: String, method: String = "GET", body: [String: Any]? = nil) async throws -> [String: Any] {
        guard let web = store.webView, store.ready, let identity = store.snapshot?.identity,
              Self.allowed(path, method: method) else { throw FleetServiceError.invalidRequest }
        let result = try await web.callAsyncJavaScript("""
            try {
              if (String(window.FleetAuth?.user?.id || '') !== identity) throw new Error('登录已变化，请重新打开设置');
              const value = await FleetAuth.json(path, {method, cache: 'no-store', ...(body ? {body} : {})}) || {};
              return {ok: true, value};
            } catch (error) {
              return {ok: false, message: String(error?.message || '请求失败，请重试。')};
            }
            """, arguments: ["identity": identity, "path": path, "method": method, "body": (body as Any?) ?? NSNull()], in: nil, contentWorld: .page)
        guard store.ready, store.snapshot?.identity == identity else { throw FleetServiceError.identityChanged }
        guard let response = result as? [String: Any], response["ok"] as? Bool == true else {
            throw FleetRequestError(message: (result as? [String: Any])?["message"] as? String ?? "请求失败，请重试。")
        }
        return response["value"] as? [String: Any] ?? [:]
    }

    func shared(_ action: String, values: [String: Any] = [:]) async throws -> Any? {
        guard let web = store.webView, store.ready, let identity = store.snapshot?.identity else { throw FleetServiceError.invalidRequest }
        let result = try await web.callAsyncJavaScript("""
            if (String(window.FleetAuth?.user?.id || '') !== identity) throw new Error('登录已变化，请重新打开设置');
            switch (action) {
              case 'refresh':
                await refreshSettings(); await refreshNodes(); await refreshDeviceAppearance(); await FleetTheme.refreshPalette();
                FleetNativeAppearance?.publish(); FleetWorkspace.publish(); return null;
              case 'palette': return {palette: FleetTheme.getPalette(), defaults: FleetTheme.paletteDefaults};
              case 'save-palette': await FleetTheme.savePalette(values.palette); FleetNativeAppearance?.publish(); return null;
              case 'device-appearance': await FleetDeviceAppearance.save(values.id, values.appearance, '/api/settings'); await refreshDeviceAppearance(); return null;
              case 'cache': return chatCacheStats();
              case 'binding-sessions': return await fetchAccessKeyBindingSessions(values.device, values.client);
              case 'access-key-request': return accessKeyCurl(values.key, location.origin);
              case 'logout': await FleetAuth.logout(); return null;
              case 'reauthenticate': FleetAuth.invalidate({returnTo: '/'}); return null;
              default: throw new Error('不支持的操作');
            }
            """, arguments: ["identity": identity, "action": action, "values": values], in: nil, contentWorld: .page)
        if !["logout", "reauthenticate"].contains(action), (!store.ready || store.snapshot?.identity != identity) { throw FleetServiceError.identityChanged }
        return result
    }

    static func allowed(_ path: String, method: String) -> Bool {
        guard ["GET", "POST", "PUT", "PATCH", "DELETE"].contains(method),
              let components = URLComponents(string: path), components.scheme == nil, components.host == nil,
              components.fragment == nil, components.path.hasPrefix("/"), !path.contains(".."), !path.contains("%") else { return false }
        let route = components.path
        if route == "/api/standby/token" { return method == "POST" }
        if ["/api/settings", "/api/devices", "/api/names"].contains(route) { return method != "DELETE" }
        if route == "/enroll/client-release.json" { return method == "GET" }
        if route.range(of: "^/api/automation/access-keys(?:/[A-Za-z0-9_-]+)?$", options: .regularExpression) != nil { return true }
        if route == "/api/auth/sessions" { return method == "GET" }
        if ["/api/auth/logout-others", "/api/auth/password", "/api/auth/totp/start", "/api/auth/totp/confirm", "/api/auth/recovery-codes"].contains(route) { return method == "POST" }
        if route.range(of: "^/m[1-9][0-9]*/api/(info|proxy|projects|sessions)$", options: .regularExpression) != nil {
            return method == "GET" || (route.hasSuffix("/proxy") && method == "POST")
        }
        return false
    }

    static func copy(_ value: String, sensitive: Bool = false) {
        UIPasteboard.general.setItems([[UIPasteboard.typeAutomatic: value]], options: sensitive ? [.localOnly: true, .expirationDate: Date().addingTimeInterval(120)] : [:])
    }
}

private struct FleetRequestError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}

enum FleetServiceError: LocalizedError {
    case invalidRequest, identityChanged
    var errorDescription: String? {
        self == .identityChanged ? "登录已变化，请重新打开设置。" : "当前无法执行此操作，请检查登录和连接。"
    }
}

enum NativeSheet: Identifiable, Equatable {
    case settings, device(String), newSessionDevice, gateway
    var id: String {
        switch self {
        case .settings: return "settings"
        case .device(let identifier): return "device-\(identifier)"
        case .newSessionDevice: return "new-session-device"
        case .gateway: return "gateway"
        }
    }

}
