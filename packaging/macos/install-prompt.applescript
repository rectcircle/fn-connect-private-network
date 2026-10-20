-- Launched in the installing user's GUI session, never as root.
set promptTitle to "FnCPN installation complete"
set promptMessage to "Would you like to open FnCPN now?"
set laterButton to "Later"
set launchButton to "Open FnCPN"
if (user locale of (system info)) begins with "zh" then
    set promptTitle to "FnCPN 安装完成"
    set promptMessage to "是否现在启动 FnCPN？"
    set laterButton to "稍后"
    set launchButton to "启动 FnCPN"
end if

try
    set answer to display dialog promptMessage with title promptTitle buttons {laterButton, launchButton} default button launchButton cancel button laterButton with icon (POSIX file "/Applications/FnCPN.app/Contents/Resources/AppIcon.icns") giving up after 120
on error number -128
    return
end try
if gave up of answer then return
if button returned of answer is launchButton then
    do shell script "/usr/bin/open /Applications/FnCPN.app"
end if
