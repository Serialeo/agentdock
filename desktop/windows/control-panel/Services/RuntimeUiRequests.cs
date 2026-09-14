namespace AgentDock.ControlPanel;

// UI-thread owned: writes invalidate reads, but hiding a window never releases an active mutation.
internal sealed class RuntimeUiRequests
{
    public long Revision { get; private set; }
    public bool Visible { get; private set; } = true;
    public bool Changing { get; private set; }
    private bool _reading;
    private long _change;
    public bool IsCurrent(long ticket) => Visible && ticket == Revision;
    public long? BeginRead() {
        if (!Visible || Changing || _reading) return null;
        _reading = true;
        return ++Revision;
    }
    public void EndRead(long ticket) { if (ticket == Revision) _reading = false; }
    public long? BeginChange() {
        if (!Visible || Changing) return null;
        Changing = true;
        _reading = false;
        return _change = ++Revision;
    }
    public void EndChange(long ticket) { if (ticket == _change) Changing = false; }
    public void SetVisible(bool visible) {
        Visible = visible;
        ++Revision;
        _reading = false;
    }
}
