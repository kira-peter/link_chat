@echo off
if "%1"=="client" goto client
if "%1"=="server" goto server
echo Usage: run.bat [server^|client]
exit /b 1

:server
"%~dp0build\link-chat-server.exe" %2 %3 %4 %5
goto :eof

:client
"%~dp0build\link-chat.exe" %2 %3 %4 %5
goto :eof
