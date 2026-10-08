import SwiftUI
import WebKit

struct FleetWebView: UIViewRepresentable {
    @ObservedObject var store: FleetStore
    var layout: FleetLayout = .compact
    var sessionsPinned = true

    func makeCoordinator() -> WebCoordinator { WebCoordinator(store: store) }

    func makeUIView(context: Context) -> WKWebView {
        if let existing = store.webView { return existing }
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = store.dataStore
        configuration.allowsInlineMediaPlayback = true
        configuration.userContentController.add(context.coordinator, name: "fleet")
        configuration.userContentController.addUserScript(WKUserScript(
            source: bootstrapScript,
            injectionTime: .atDocumentStart, forMainFrameOnly: true
        ))
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.navigationDelegate = context.coordinator
        view.uiDelegate = context.coordinator
        view.scrollView.contentInsetAdjustmentBehavior = .never
        view.allowsBackForwardNavigationGestures = false
        view.isInspectable = _isDebugAssertConfiguration()
        view.accessibilityIdentifier = "session-webview"
        store.webView = view
        if let gateway = store.gateway { view.load(URLRequest(url: gateway)) }
        return view
    }

    func updateUIView(_ view: WKWebView, context: Context) {
        view.evaluateJavaScript(layoutScript)
    }

    private var layoutScript: String {
        "document.documentElement.dataset.nativeLayout = '\(layout == .compact ? "compact" : "expanded")'; document.documentElement.dataset.nativeSessionsPinned = '\(sessionsPinned)'; void 0;"
    }

    var bootstrapScript: String {
        var script = "window.__fleetNativeVersion = 1; \(layoutScript)"
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("-fleet-test-reset") {
            script += "if (location.pathname === '/') { localStorage.clear(); sessionStorage.clear(); }"
        }
        #endif
        return script
    }
}

@MainActor
final class WebCoordinator: WebInteractionCoordinator, WKScriptMessageHandler {
    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, let url = message.frameInfo.request.url,
              let gateway = store?.gateway, GatewayAddress.sameOrigin(url, gateway),
              url.path == "/" || url.path == "/index.html" else { return }
        store?.receive(message.body)
    }

    func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
        store?.reset()
        store?.loading = true
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        store?.loading = false
        guard let url = webView.url, url.path == "/" || url.path == "/index.html" else { return }
        webView.evaluateJavaScript("typeof window.FleetNative === 'object'") { [weak self] value, _ in
            if value as? Bool != true { self?.store?.error = "请先部署此分支的 dashboard，网关尚未提供 App 接入组件。" }
        }
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError failure: Error) {
        if (failure as NSError).code == NSURLErrorCancelled { return }
        store?.loading = false
        store?.error = failure.localizedDescription
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError failure: Error) {
        store?.loading = false
        store?.error = failure.localizedDescription
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        store?.reset()
        webView.reload()
    }

    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { decisionHandler(.cancel); return }
        if action.targetFrame?.isMainFrame == false { decisionHandler(.allow); return }
        if url.absoluteString == "about:blank" { decisionHandler(.allow); return }
        guard let gateway = store?.gateway else { decisionHandler(.cancel); return }
        if GatewayAddress.sameOrigin(url, gateway) {
            if action.shouldPerformDownload { decisionHandler(.download); return }
            if action.targetFrame == nil || (action.navigationType == .linkActivated && url.path == "/view") {
                store?.auxiliaryPage = WebPage(url: url)
                decisionHandler(.cancel)
            } else { decisionHandler(.allow) }
        } else {
            if action.navigationType == .linkActivated, ["https", "http", "mailto", "tel"].contains(url.scheme ?? "") {
                UIApplication.shared.open(url)
            } else { store?.error = "网关跳转到其他站点，已阻止在 App 内加载。" }
            decisionHandler(.cancel)
        }
    }

}

@MainActor
class WebInteractionCoordinator: NSObject, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
    weak var store: FleetStore?
    private var destinations: [ObjectIdentifier: (url: URL, identity: String)] = [:]

    init(store: FleetStore) { self.store = store }

    func webView(_ webView: WKWebView, decidePolicyFor response: WKNavigationResponse,
                 decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        decisionHandler(response.canShowMIMEType ? .allow : .download)
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse,
                  suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        guard let store, store.ready, let identity = store.snapshot?.identity,
              let gateway = store.gateway, let source = response.url,
              GatewayAddress.sameOrigin(source, gateway) else { completionHandler(nil); return }
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        do {
            try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
            let filename = (suggestedFilename as NSString).lastPathComponent
            let destination = folder.appendingPathComponent(filename.isEmpty ? "download" : filename)
            destinations[ObjectIdentifier(download)] = (destination, identity)
            completionHandler(destination)
        } catch { completionHandler(nil); store.error = error.localizedDescription }
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let destination = destinations.removeValue(forKey: ObjectIdentifier(download)) else { return }
        guard let store, store.ready, store.snapshot?.identity == destination.identity else {
            try? FileManager.default.removeItem(at: destination.url.deletingLastPathComponent())
            return
        }
        store.discardExport()
        store.exportedFile = SharedFile(url: destination.url)
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        if let destination = destinations.removeValue(forKey: ObjectIdentifier(download)) {
            try? FileManager.default.removeItem(at: destination.url.deletingLastPathComponent())
            if store?.snapshot?.identity == destination.identity { store?.error = error.localizedDescription }
        }
    }

    private func present(_ alert: UIAlertController) -> Bool {
        guard let scene = UIApplication.shared.connectedScenes.first(where: { $0.activationState == .foregroundActive }) as? UIWindowScene,
              var presenter = scene.windows.first(where: \.isKeyWindow)?.rootViewController else { return false }
        while let presented = presenter.presentedViewController { presenter = presented }
        presenter.present(alert, animated: true)
        return true
    }

    func webView(_ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping () -> Void) {
        let alert = UIAlertController(title: "Fleet Hub", message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "确定", style: .default) { _ in completionHandler() })
        if !present(alert) { completionHandler() }
    }

    func webView(_ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void) {
        let alert = UIAlertController(title: "Fleet Hub", message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "取消", style: .cancel) { _ in completionHandler(false) })
        alert.addAction(UIAlertAction(title: "确定", style: .default) { _ in completionHandler(true) })
        if !present(alert) { completionHandler(false) }
    }

    func webView(_ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String, defaultText: String?,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (String?) -> Void) {
        let alert = UIAlertController(title: "Fleet Hub", message: prompt, preferredStyle: .alert)
        alert.addTextField { $0.text = defaultText }
        alert.addAction(UIAlertAction(title: "取消", style: .cancel) { _ in completionHandler(nil) })
        alert.addAction(UIAlertAction(title: "确定", style: .default) { _ in completionHandler(alert.textFields?.first?.text) })
        if !present(alert) { completionHandler(nil) }
    }
}

struct AuxiliaryWebView: UIViewRepresentable {
    let url: URL
    @ObservedObject var store: FleetStore
    func makeCoordinator() -> AuxiliaryCoordinator { AuxiliaryCoordinator(origin: url, store: store) }

    func makeUIView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = store.dataStore
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.navigationDelegate = context.coordinator
        view.uiDelegate = context.coordinator
        view.accessibilityIdentifier = "auxiliary-webview"
        view.load(URLRequest(url: url))
        return view
    }

    func updateUIView(_ view: WKWebView, context: Context) {}
}

@MainActor
final class AuxiliaryCoordinator: WebInteractionCoordinator {
    private let origin: URL

    init(origin: URL, store: FleetStore) {
        self.origin = origin
        super.init(store: store)
    }

    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url else { decisionHandler(.cancel); return }
        if url.absoluteString == "about:blank" || GatewayAddress.sameOrigin(origin, url) {
            if action.targetFrame?.isMainFrame != false && ["/", "/index.html"].contains(url.path) {
                store?.auxiliaryPage = nil
                store?.foreground()
                decisionHandler(.cancel)
            } else if action.targetFrame?.isMainFrame != false && ["/auth", "/auth/"].contains(url.path) {
                store?.reset()
                store?.webView?.load(action.request)
                decisionHandler(.cancel)
            } else if action.shouldPerformDownload { decisionHandler(.download) }
            else if action.targetFrame == nil { webView.load(URLRequest(url: url)); decisionHandler(.cancel) }
            else { decisionHandler(.allow) }
        } else {
            if action.navigationType == .linkActivated && ["https", "http", "mailto", "tel"].contains(url.scheme ?? "") {
                UIApplication.shared.open(url)
            }
            decisionHandler(.cancel)
        }
    }
}

struct ShareFileView: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: [url], applicationActivities: nil)
    }

    func updateUIViewController(_ controller: UIActivityViewController, context: Context) {}
}
