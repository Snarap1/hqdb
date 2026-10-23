# Records the demo with VHS (https://github.com/charmbracelet/vhs). Needs the
# Docker fixtures up (bash testdata/fixtures.sh up); then, from the
# repository root, record and convert it to docs/demo.gif:
#
#   vhs docs/demo.tape && ffmpeg -y -i /tmp/hqdb-demo/demo.mp4 \
#     -vf "fps=12,scale=1000:-1:flags=lanczos,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4" \
#     docs/demo.gif
#
# VHS writes the mp4 rather than the gif because its own gif encoder emits an
# empty file for a recording this long.

Output "/tmp/hqdb-demo/demo.mp4"

Set Shell bash
Set FontSize 16
Set Width 1400
Set Height 760
Set Padding 16
Set TypingSpeed 45ms
Set Theme "Catppuccin Mocha"

Hide
Type "go build -o /tmp/hqdb-demo/hqdb . && export PATH=/tmp/hqdb-demo:$PATH HQDB_CONFIG=$PWD/testdata/demo.toml XDG_STATE_HOME=/tmp/hqdb-demo/state && rm -rf /tmp/hqdb-demo/state && clear"
Enter
Sleep 8s
Show

# The config: two postgres Profiles over the same database, one read-only
# for analysts and one read-write for developers, plus mongodb and redis.
Type "hqdb check"
Sleep 300ms
Enter
Sleep 3s

Type "hqdb"
Sleep 300ms
Enter
Sleep 1500ms

# Drill down to a Table and open its Data view.
Enter
Sleep 700ms
Down
Enter
Sleep 700ms
Down 2
Enter
Sleep 700ms
Down
Sleep 300ms
Enter
Sleep 2s

# Server-side WHERE and ORDER BY.
Type "f"
Type "price < 2"
Enter
Sleep 1500ms
Type "o"
Type "price DESC"
Enter
Sleep 2s

# A Console on the read-only Profile refuses a write.
Shift+Tab
Up 4
Enter
Sleep 500ms
Type "c"
Sleep 800ms
Type "DELETE FROM widgets WHERE price < 1;"
Sleep 300ms
Alt+Enter
Sleep 3s
Escape

# The same write on the read-write Profile asks first.
Shift+Tab
Down
Type "c"
Sleep 800ms
Type "DELETE FROM widgets WHERE price < 1;"
Sleep 300ms
Alt+Enter
Sleep 3s
Type "n"
Sleep 1500ms
Escape

# A mongodb Collection opens as Document cards.
Shift+Tab
Down
Enter
Sleep 1s
Down
Enter
Sleep 1s
Down 2
Enter
Sleep 3500ms
