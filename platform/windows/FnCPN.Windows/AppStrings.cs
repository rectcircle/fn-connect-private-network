using System.Globalization;
using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;

namespace FnCPN.Windows;

internal static partial class AppStrings
{
    public static bool IsEnglish => !CultureInfo.CurrentUICulture.TwoLetterISOLanguageName.Equals("zh", StringComparison.OrdinalIgnoreCase);
    public static string Get(string chinese) => IsEnglish && English.TryGetValue(chinese, out var english) ? english : chinese;

    public static void ApplyToTree(DependencyObject root)
    {
        if (root is TextBlock text) text.Text = Get(text.Text);
        if (root is ContentControl content && content.Content is string value) content.Content = Get(value);
        var automationName = AutomationProperties.GetName(root);
        if (!string.IsNullOrWhiteSpace(automationName)) AutomationProperties.SetName(root, Get(automationName));
        for (var index = 0; index < System.Windows.Media.VisualTreeHelper.GetChildrenCount(root); index++)
            ApplyToTree(System.Windows.Media.VisualTreeHelper.GetChild(root, index));
    }
}
