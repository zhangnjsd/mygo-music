package app

import "github.com/egoist/mygo/ui"

type keyringHelpDialog struct {
	open   bool
	scroll ui.ScrollState
}

type keyringCommand struct {
	name, command string
}

var keyringInstallCommands = []keyringCommand{
	{"Ubuntu / Debian / Linux Mint", "sudo apt install gnome-keyring libsecret-1-0"},
	{"Arch Linux / Manjaro / EndeavourOS", "sudo pacman -S gnome-keyring libsecret"},
	{"Fedora", "sudo dnf install gnome-keyring libsecret"},
	{"openSUSE", "sudo zypper install gnome-keyring libsecret-1-0"},
}

const keyringStartCommand = "gnome-keyring-daemon --start --components=secrets"

func (a *app) keyringHelpDialog(c *ui.Context) {
	d, p := &a.keyringHelp, a.pal
	ui.DialogBase(c.Key("keyring-help"), &d.open, func(backdrop, panel ui.Element) {
		w, h := c.Size()
		backdrop.Background(ui.RGBA(0, 0, 0, .35))
		panel.Width(min(600, w-48)).Padding(16, 24, 24, 24).Radius(18).Gap(12).
			Background(p.bg).Border(1, p.hairline).Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, .4)).
			Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: .94}, Exit: &ui.Motion{Scale: .96}})
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, "安装并启用 Linux 凭据服务").Grow(1).MinWidth(0).MaxLines(2).FontSize(17).FontWeight(700)
			iconButton(c.Key("close"), iconX, 18, p.muted, "关闭").Shrink(0).OnClick(func() { d.open = false })
		})
		ui.Scroll(c.Key("instructions")).TrackScroll(&d.scroll).MaxHeight(min(500, h-140)).Gap(16).Padding(0, 10, 0, 0).Children(func() {
			ui.Text(c, "系统凭据服务不可用，无法保存登录状态。本次登录仅在应用运行期间有效，重启应用后需要重新登录。").FontSize(13).LineHeight(1.5).TextColor(p.muted)
			ui.Text(c, "请安装并启用支持 Secret Service 的 keyring。以下以 GNOME Keyring 为例，复制对应发行版的命令并在终端中执行：").FontSize(13).LineHeight(1.5)
			for _, command := range keyringInstallCommands {
				a.keyringCommand(c.Key(command.name), command)
			}
			ui.Text(c, "安装后，在桌面的启动项中启用 GNOME Keyring 的密码服务，注销并重新登录桌面，按系统提示创建或解锁登录密钥环。").FontSize(13).LineHeight(1.5)
			ui.Text(c, "精简桌面或窗口管理器用户：请确认图形会话有可用的 D-Bus 会话总线，并将以下命令加入会话自启动；也可先在当前会话的终端中执行：").FontSize(13).LineHeight(1.5)
			a.keyringCommand(c.Key("start"), keyringCommand{"启动密码服务", keyringStartCommand})
			ui.Text(c, "如果已使用 KWallet 或 KeePassXC，请启用其 Secret Service 支持并解锁，无需再启动另一个 keyring 服务。\n\n安装并启用服务后，请完全退出并重新打开应用，再扫码登录以保存凭据。").FontSize(13).LineHeight(1.5).TextColor(p.muted)
		})
	})
}

func (a *app) keyringCommand(c *ui.Context, command keyringCommand) {
	p := a.pal
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, command.name).FontSize(13).FontWeight(600)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(10).Radius(8).Background(p.sidebar).Children(func() {
			ui.Text(c, command.command).Grow(1).MinWidth(0).FontSize(12.5).LineHeight(1.5).Selectable()
			pillButton(c.Key("copy"), nil, "复制", p.hover.Over(p.bg), p.text).Shrink(0).
				Label("复制 " + command.name + " 命令").OnClick(func() {
				if err := copyText(command.command); err != nil {
					a.toast = "复制失败，请选中命令后手动复制"
				} else {
					a.toast = "命令已复制"
				}
			})
		})
	})
}
