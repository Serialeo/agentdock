// Owned by the UI thread. Closing invalidates responses without unlocking a running change.
final class RuntimeUIRequests {
    private(set) var revision = 0
    private(set) var visible = true
    private(set) var changing = false
    private var reading = false
    private var change = 0
    func isCurrent(_ ticket: Int) -> Bool { visible && ticket == revision }
    func beginRead() -> Int? {
        guard visible, !changing, !reading else { return nil }
        reading = true; revision += 1; return revision
    }
    func endRead(_ ticket: Int) { if ticket == revision { reading = false } }
    func beginChange() -> Int? {
        guard visible, !changing else { return nil }
        changing = true; reading = false; revision += 1; change = revision; return change
    }
    func endChange(_ ticket: Int) { if ticket == change { changing = false } }
    func setVisible(_ value: Bool) { visible = value; revision += 1; reading = false }
}
