import Foundation

extension AgentStatus {
    public func isHealthyAfterUpdate(version expectedVersion: String, build: Int64, replacingPID: Int?) -> Bool {
        guard schema == 1, settings.schema == 1, pid > 0, pid != replacingPID,
              version == "\(expectedVersion)+\(build)" else { return false }
        switch runtime?.phase {
        case "unbound": return binding == nil
        case "running": return binding?.complete == true && binding?.locked == false
        default: return false
        }
    }
}
