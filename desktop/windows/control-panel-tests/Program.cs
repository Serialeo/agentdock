using AgentDock.ControlPanel;
static void Check(bool value) { if (!value) throw new Exception("UI request ordering failed"); }
var state = new RuntimeUiRequests();
var oldRead = state.BeginRead()!.Value;
Check(state.BeginRead() == null); // slow reads cannot accumulate
var write = state.BeginChange()!.Value;
Check(!state.IsCurrent(oldRead)); // delayed GET cannot overwrite POST
state.EndRead(oldRead);
Check(state.Changing && state.BeginRead() == null && state.BeginChange() == null);
state.SetVisible(false);
state.SetVisible(true);
Check(!state.IsCurrent(write) && state.Changing); // closing/reopening does not unlock the write
state.EndChange(write);
var recovered = state.BeginRead()!.Value;
Check(state.IsCurrent(recovered));
state.EndRead(recovered);
var restart = state.BeginChange()!.Value;
Check(state.BeginChange() == null && state.BeginRead() == null);
state.EndChange(restart);
Check(state.BeginRead() != null);
Console.WriteLine("Windows runtime UI ordering tests passed");
