# Run from the repo root after make build. See docs/demo.md.
Output docs/demo.gif
Require baton
Set Shell "bash"
Set FontSize 18
Set Width 1100
Set Height 620
Set TypingSpeed 25ms

Hide
# Keep the demo out of the real HOME, including inherited XDG locations.
Type `demo_root=$(mktemp -d); export BATON_DEMO_ROOT=$demo_root; export PATH=$PWD/bin:$PATH`
Enter
Type `for name in $(env | sed -n 's/^\(XDG_[^=]*\)=.*/\1/p'); do unset "$name"; done`
Enter
Type `export HOME=$demo_root USERPROFILE=$demo_root XDG_CONFIG_HOME=$demo_root/config XDG_STATE_HOME=$demo_root/state XDG_CACHE_HOME=$demo_root/cache XDG_DATA_HOME=$demo_root/data XDG_RUNTIME_DIR=$demo_root/runtime`
Enter
Type `mkdir -p "$XDG_CONFIG_HOME/baton"; printf '[lanes.demo]\nslots = 1\nmemory_gate = false\n' > "$XDG_CONFIG_HOME/baton/config.toml"`
Enter
# This path was created above; cleanup never targets a user's existing tree.
Type `trap 'rm -rf "$BATON_DEMO_ROOT"' EXIT`
Enter
Type "clear"
Enter
Show

Type "baton run --lane demo --label first -- sleep 4 &"
Enter
Sleep 500ms
Type "baton run --lane demo --label second -- sleep 2 &"
Enter
Sleep 500ms
Type "baton status"
Enter
Sleep 5s
Type "wait; baton history --since 1h --lane demo"
Enter
Sleep 3s
