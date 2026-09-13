using System.ComponentModel;
using System.Windows;

namespace AgentDock.ControlPanel;

public partial class UpdateProgressWindow : Window
{
    private bool _canClose;

    public UpdateProgressWindow(string currentVersion, string latestVersion)
    {
        InitializeComponent();
        VersionText.Text = $"{currentVersion} → {latestVersion}";
    }

    public void Report(UpdateProgress progress)
    {
        UpdateProgressBar.Value = Math.Max(UpdateProgressBar.Value, Math.Clamp(progress.Percentage, 0, 100));
        StatusText.Text = progress.Message;
    }

    public void Complete(string message)
    {
        _canClose = true;
        UpdateProgressBar.Value = 100;
        StatusText.Text = message;
        CloseButton.IsEnabled = true;
        CloseButton.Focus();
    }

    public void Fail(string message)
    {
        _canClose = true;
        StatusText.Text = message;
        CloseButton.IsEnabled = true;
        CloseButton.Focus();
    }

    protected override void OnClosing(CancelEventArgs e)
    {
        if (!_canClose)
        {
            e.Cancel = true;
            return;
        }
        base.OnClosing(e);
    }

    private void CloseButton_Click(object sender, RoutedEventArgs e) => Close();
}
