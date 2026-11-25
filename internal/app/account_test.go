package app

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"netease-music-mygo/internal/netease"
)

type confirmedQR struct {
	committed bool
}

func (q *confirmedQR) Check(string) (netease.QRStatus, error) {
	return netease.QRStatus{Code: netease.QRConfirmed}, nil
}

func (q *confirmedQR) Commit() { q.committed = true }

// A confirmation queued before the dialog closes or is replaced cannot change the active account.
func TestStaleQRConfirmationKeepsActiveAccount(t *testing.T) {
	for _, reopen := range []bool{false, true} {
		name := "closed"
		if reopen {
			name = "reopened"
		}
		t.Run(name, func(t *testing.T) {
			a := newTestApp()
			posts := testPosts(a)
			a.login = loginState{open: true, gen: 1}
			previous := &netease.Profile{UserID: 42}
			a.profile = previous
			queue := make(chan func(playReporter), 1)
			a.reportQueue = queue
			tt := ui.NewTester(a.loginDialog, 800, 600)
			tt.Frame()
			tt.Advance(300 * time.Millisecond)

			attempt := &confirmedQR{}
			stopped := make(chan bool, 1)
			go func() { stopped <- a.checkLogin(1, "test-key", attempt) }()
			apply := wait(t, posts, "the queued confirmation")
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			if a.login.open {
				t.Error("Escape did not close the dialog")
			}
			if reopen {
				a.login = loginState{open: true, gen: 2}
			}
			apply()
			if !wait(t, stopped, "the stale poll to stop") {
				t.Error("the stale attempt would keep polling")
			}
			if attempt.committed || a.profile != previous || a.reportQueue != queue || a.login.finished {
				t.Fatal("a stale confirmation changed the account, its report queue, or the current dialog")
			}
		})
	}
}

// A keychain failure does not announce a durable login or close its dialog.
func TestLoginSaveFailureStaysVisible(t *testing.T) {
	save := saveLoginSession
	t.Cleanup(func() { saveLoginSession = save })
	saveLoginSession = func() error { return errors.New("keychain unavailable") }
	a := newTestApp()
	posts := testPosts(a)
	a.login = loginState{open: true, gen: 1, finished: true}
	a.saveLogin(func() {
		a.login.open = false
	})
	wait(t, posts, "the failed session save")()
	if a.login.finished || !a.login.open || a.login.err == "" {
		t.Fatalf("failed session save reported success: %+v", a.login)
	}
}

// A failed save for the current account is visible without a QR dialog.
func TestLoginSaveFailureWithoutDialogShowsToast(t *testing.T) {
	save := saveLoginSession
	t.Cleanup(func() { saveLoginSession = save })
	saveLoginSession = func() error { return errors.New("keychain unavailable") }
	a := newTestApp()
	posts := testPosts(a)
	a.profile = &netease.Profile{UserID: 1}
	a.saveLogin(nil)
	wait(t, posts, "the failed session save")()
	if a.toast == "" {
		t.Fatal("the current account's session save failure was hidden")
	}
}

// Saving the credentials is what allows the successful login dialog to close.
func TestLoginSaveSuccessClosesDialog(t *testing.T) {
	save := saveLoginSession
	t.Cleanup(func() { saveLoginSession = save })
	saveLoginSession = func() error { return nil }
	a := newTestApp()
	posts := testPosts(a)
	a.login = loginState{open: true, gen: 1}
	a.saveLogin(func() {
		a.login.finished = true
		a.login.open = false
	})
	wait(t, posts, "the successful session save")()
	if !a.login.finished || a.login.open || a.login.err != "" {
		t.Fatalf("saved session did not finish the login: %+v", a.login)
	}
}

// Only an unavailable Linux credential service replaces the QR dialog with setup guidance, preserving the active account.
func TestLoginSaveFailureGuidance(t *testing.T) {
	for _, tc := range []struct {
		name, goos string
		err        error
		guide      bool
	}{
		{"linux-missing", "linux", fmt.Errorf("secrets: %w", errors.ErrUnsupported), true},
		{"linux-denied", "linux", errors.New("unlock denied"), false},
		{"linux-no-cookie", "linux", errors.New("no session cookie"), false},
		{"macos", "darwin", errors.ErrUnsupported, false},
		{"windows", "windows", errors.ErrUnsupported, false},
	} {
		for _, dialog := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dialog=%t", tc.name, dialog), func(t *testing.T) {
				a := newTestApp()
				profile := &netease.Profile{UserID: 42}
				a.profile = profile
				a.login = loginState{open: dialog, gen: 7, finished: true}
				a.loginSaveFailed(tc.err, dialog, tc.goos)
				if a.keyringHelp.open != tc.guide || a.login.open != (dialog && !tc.guide) {
					t.Fatalf("guide open=%t, QR open=%t", a.keyringHelp.open, a.login.open)
				}
				if a.profile != profile || a.login.gen != 7 {
					t.Fatal("save failure changed the active login")
				}
				if dialog && (a.login.finished || a.login.err == "") || !dialog && a.toast == "" {
					t.Fatal("save failure was hidden or announced as a durable login")
				}
			})
		}
	}
}

// A save failure delivered after sign-out cannot show instructions for the previous account.
func TestStaleLoginSaveFailureIsIgnored(t *testing.T) {
	save := saveLoginSession
	t.Cleanup(func() { saveLoginSession = save })
	saveLoginSession = func() error { return errors.ErrUnsupported }
	a := newTestApp()
	posts := testPosts(a)
	a.login = loginState{open: true, gen: 1}
	a.saveLogin(func() { t.Error("failed save reported success") })
	apply := wait(t, posts, "the failed session save")
	a.login = loginState{gen: 2}
	a.homeGen++
	apply()
	if a.login.err != "" || a.toast != "" || a.keyringHelp.open {
		t.Fatal("an old account's save failure changed the current UI")
	}
}

// Dismissing the setup guide keeps the current login, including when the text must scroll in a small window.
func TestKeyringGuideDismissal(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			a := newTestApp()
			profile := &netease.Profile{UserID: 42}
			a.profile = profile
			a.login = loginState{open: true}
			a.loginSaveFailed(errors.ErrUnsupported, true, "linux")
			tt := ui.NewTester(func(c *ui.Context) {
				a.pal = paletteFor(c.Theme())
				a.keyringHelpDialog(c)
			}, 640, 480)
			tt.SetDark(dark)
			tt.Frame()
			tt.Advance(time.Second)
			tt.Frame()
			if dir := os.Getenv("NM_FRAMES"); dir != "" {
				for _, end := range []bool{false, true} {
					if end {
						tt.Scroll(320, 280, 0, 10000)
						tt.Frame()
					}
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("keyring-guide-dark-%t-end-%t.png", dark, end)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(f, tt.Image())
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := tt.Click("关闭"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if a.keyringHelp.open || a.login.open || a.profile != profile {
				t.Fatal("dismissing instructions changed the login or left a modal open")
			}
		})
	}
}

// Every copy button writes only its command to the clipboard, even after the guide scrolls.
func TestKeyringGuideCopiesCommands(t *testing.T) {
	copy := copyText
	t.Cleanup(func() { copyText = copy })
	var copied string
	var copyErr error
	copyText = func(text string) error { copied = text; return copyErr }
	a := newTestApp()
	a.keyringHelp.open = true
	tt := ui.NewTester(func(c *ui.Context) {
		a.pal = paletteFor(c.Theme())
		a.keyringHelpDialog(c)
	}, 640, 480)
	tt.Frame()
	tt.Advance(time.Second)
	commands := append([]keyringCommand(nil), keyringInstallCommands...)
	commands = append(commands, keyringCommand{"启动密码服务", keyringStartCommand})
	for _, command := range commands {
		// Bring each command into view without relying on the wording of the instructions.
		found := false
		for range 20 {
			if err := tt.Click("复制 " + command.name + " 命令"); err == nil {
				found = true
				break
			}
			tt.Scroll(320, 280, 0, 60)
			tt.Frame()
		}
		if !found || copied != command.command {
			t.Fatalf("copy %s: got %q, want %q (button reached=%t)", command.name, copied, command.command, found)
		}
		if !a.keyringHelp.open || a.toast == "" {
			t.Fatal("copying closed the guide or gave no feedback")
		}
	}
	copyErr = errors.New("clipboard unavailable")
	a.toast = ""
	if err := tt.Click("复制 启动密码服务 命令"); err != nil {
		t.Fatal(err)
	}
	if a.toast == "" || !a.keyringHelp.open {
		t.Fatal("clipboard failure was hidden or closed the guide")
	}
}

// Clicking the account row drops its menu, which takes the focus so the
// arrows walk its items, and Escape or a second click puts it away.
func TestAccountMenu(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.profile = &netease.Profile{UserID: 1, Nickname: "小明"}
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if err := tt.Click("小明"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.accountOpen || !tt.HasText("退出登录") {
		t.Fatalf("menu not open: %v", a.accountOpen)
	}
	if !tt.Focused("我的主页") {
		t.Fatal("the first item has no focus")
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if !tt.Focused("退出登录") {
		t.Fatal("Down did not move to signing out")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.accountOpen {
		t.Fatal("Escape left the menu open")
	}

	if err := tt.Click("小明"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.accountOpen {
		t.Fatal("the second opening did not show the account menu")
	}
	if err := tt.Click("小明"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.accountOpen {
		t.Fatal("a second click left the menu open")
	}
}

// 我的主页 in the account menu opens the page of who is signed in and puts
// the menu away.
func TestAccountMenuOpensOwnPage(t *testing.T) {
	fakeSubscriptions(t)
	detail, playlists := userDetail, userPlaylists
	defer func() { userDetail, userPlaylists = detail, playlists }()
	userDetail = func(id int64) (*netease.User, error) { return &netease.User{UserID: id, Nickname: "小明"}, nil }
	userPlaylists = func(int64) ([]netease.Playlist, []netease.Playlist, error) { return nil, nil, nil }

	a := newTestApp()
	posts := testPosts(a)
	a.settings = defaultSettings()
	a.profile = &netease.Profile{UserID: 42, Nickname: "小明"}
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	tt.Click("小明")
	tt.Frame()
	if err := tt.Click("我的主页"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.route.page != pageUser || a.route.id != 42 {
		t.Fatalf("route = %+v, want the page of user 42", a.route)
	}
	if a.accountOpen {
		t.Fatal("the menu stayed open")
	}
	for d := a.users.get(42); d.loading > 0 || d.subsLoading > 0; {
		refreshPost(t, posts)
	}
}

// fakeSubscriptions answers for the artists the listener follows, the
// podcasts they subscribe to and the albums they saved, and tells on each
// time the artists are asked for.
func fakeSubscriptions(t *testing.T) chan struct{} {
	asked := make(chan struct{}, 8)
	ars, pods, als := followedArtists, subscribedPodcasts, subscribedAlbums
	t.Cleanup(func() { followedArtists, subscribedPodcasts, subscribedAlbums = ars, pods, als })
	followedArtists = func() ([]netease.ArtistProfile, error) { asked <- struct{}{}; return nil, nil }
	subscribedPodcasts = func() ([]netease.Podcast, error) { return nil, nil }
	subscribedAlbums = func() ([]netease.AlbumProfile, error) { return nil, nil }
	return asked
}

// ownPageApp is signed in as user 9, whose page has what they follow.
func ownPageApp() *app {
	a := newTestApp()
	a.settings = defaultSettings()
	a.profile = &netease.Profile{UserID: 9, Nickname: "听歌的人"}
	user := netease.User{UserID: 9, Nickname: "听歌的人", Signature: "在音乐里，寻找同路的人。", Follows: 12, Followeds: 24}
	a.users.set(9, &userData{
		user:      &user,
		playlists: []netease.Playlist{{ID: 30, Name: "通勤路上", TrackCount: 40}},
		artists:   []netease.ArtistProfile{{Artist: netease.Artist{ID: 7, Name: "MyGO!!!!!"}}, {Artist: netease.Artist{ID: 10, Name: "Ave Mujica"}}},
		podcasts: []netease.Podcast{
			{ID: 5, Name: "夜航电台", DJ: netease.User{Nickname: "主播"}, ProgramCount: 120, Description: "每晚陪你说说话。"},
			{ID: 6, Name: "通勤十分钟", ProgramCount: 48},
		},
		artistsShown: true, podcastsShown: true,
	})
	return a
}

// The listener's own page asks again for what they follow each time it
// shows, as they may have followed someone since, and a followed artist
// opens their page.
func TestOwnPageFollows(t *testing.T) {
	asked := fakeSubscriptions(t)
	artist := artistDetail
	t.Cleanup(func() { artistDetail = artist })
	artistDetail = func(id int64) (*netease.ArtistProfile, error) {
		return &netease.ArtistProfile{Artist: netease.Artist{ID: id}}, nil
	}
	a := ownPageApp()
	posts := testPosts(a)
	a.open(route{page: pageUser, id: 9})
	wait(t, asked, "the artists followed")
	tt := ui.NewTester(a.view, 1200, 1200)
	tt.Frame()
	if err := tt.Click("Ave Mujica"); err != nil {
		t.Fatal(err)
	}
	if a.route.page != pageArtist || a.route.id != 10 {
		t.Fatalf("route = %+v, want the page of artist 10", a.route)
	}
	for a.users.get(9).subsLoading > 0 || a.artists.get(10).loading {
		refreshPost(t, posts)
	}
	a.back()
	wait(t, asked, "the artists followed, again")
	for a.users.get(9).subsLoading > 0 {
		refreshPost(t, posts)
	}
}

// Someone else's page doesn't ask for what the listener follows, nor shows
// it.
func TestOthersPageHidesFollows(t *testing.T) {
	asked := fakeSubscriptions(t)
	a := ownPageApp()
	a.profile = &netease.Profile{UserID: 1}
	a.open(route{page: pageUser, id: 9})
	tt := ui.NewTester(a.view, 1200, 1200)
	tt.Frame()
	if len(asked) > 0 {
		t.Fatal("asked for what the listener follows")
	}
	if err := tt.Click("Ave Mujica"); err == nil {
		t.Fatal("the listener's artists showed on someone else's page")
	}
}

// The listener's own page asks for no frames once shown.
func TestOwnPageFrameBudget(t *testing.T) {
	fakeSubscriptions(t)
	a := ownPageApp()
	posts := testPosts(a)
	a.open(route{page: pageUser, id: 9})
	for a.users.get(9).subsLoading > 0 {
		refreshPost(t, posts)
	}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Advance(time.Second)
	tt.Frame()
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Error("the page asks for frames while idle")
	}
}

// With NM_FRAMES set, saves the listener's own page, scrolled to what they
// follow, in the light and the dark.
func TestOwnPageFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	fakeSubscriptions(t)
	day := time.Date(2012, 7, 1, 0, 0, 0, 0, time.Local).UnixMilli()
	for _, dark := range []bool{false, true} {
		a := ownPageApp()
		// The albums they saved were just loaded, so the page does not ask
		// for them again.
		a.subAlbums = []netease.AlbumProfile{
			{Album: netease.Album{ID: 77, Name: "神的游戏"}, Artists: []netease.Artist{{ID: 3, Name: "张悬"}}, PublishTime: day},
			{Album: netease.Album{ID: 78, Name: "城市"}, Artists: []netease.Artist{{ID: 3, Name: "张悬"}}, PublishTime: day + 86400000},
		}
		a.subsShown = true
		a.listRefresh.albums.at = time.Now()
		a.open(route{page: pageUser, id: 9})
		tt := ui.NewTester(a.view, 1200, 1200)
		tt.SetDark(dark)
		tt.Advance(time.Second)
		tt.Frame()
		name := "user-own.png"
		if dark {
			name = "user-own-dark.png"
		}
		f, _ := os.Create(filepath.Join(dir, name))
		png.Encode(f, tt.Image())
		f.Close()
	}
}

// With NM_FRAMES set, saves the foot of the sidebar with the account menu
// open, light and dark.
func TestAccountMenuFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, dark := range []bool{false, true} {
		a := newTestApp()
		a.settings = defaultSettings()
		a.profile = &netease.Profile{UserID: 1, Nickname: "小明"}
		tt := ui.NewTester(a.view, 1000, 600)
		tt.SetDark(dark)
		tt.Frame()
		tt.Click("小明")
		tt.Advance(time.Second)
		tt.Frame()
		img := tt.Image()
		s := float32(img.Bounds().Dx()) / 1000
		crop := image.Rect(0, int(400*s), int((sidebarWidth+20)*s), img.Bounds().Dy())
		out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
		draw.Draw(out, out.Bounds(), img, crop.Min, draw.Src)
		name := "account-light.png"
		if dark {
			name = "account-dark.png"
		}
		f, _ := os.Create(filepath.Join(dir, name))
		png.Encode(f, out)
		f.Close()
	}
}
