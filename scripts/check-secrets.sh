#!/usr/bin/env bash
# =============================================================================
# ตรวจว่าไม่มีความลับหลุดเข้า repo
# =============================================================================
# ของเดิมตรวจแค่ "ชื่อไฟล์" ซึ่งจับ .pem กับ kubeconfig ได้ แต่ไม่เห็นรหัสผ่าน
# ที่ฝังอยู่ใน .sh ธรรมดา — ซึ่งเป็นรูปแบบที่หลุดจริงมาแล้ว (VT-112)
#
#   echo 'รหัสจริง' | sudo -S k3s ctr images import ...
#
# ไฟล์นั้นชื่อธรรมดาทุกอย่าง ไม่มี pattern ไหนใน .gitignore จับได้เลย
# =============================================================================
set -uo pipefail
fail=0
say() { printf '\033[1m%s\033[0m\n' "$1"; }

files=$(git ls-files)

# --- 1. ชื่อไฟล์ที่ไม่ควรอยู่ใน repo ---------------------------------------
if bad=$(echo "$files" | grep -E '\.(pem|key|p12|pfx|jks)$|(^|/)kubeconfig|\.kube/config' | grep -v '/public\.pem$'); then
  say "🔴 พบไฟล์ที่ไม่ควรอยู่ใน repo"; echo "$bad" | sed 's/^/    /'; fail=1
fi

# --- 2. เนื้อไฟล์ -----------------------------------------------------------
# แต่ละ pattern ต้องเป็นของที่ "เป็นความลับแน่ๆ" ไม่ใช่แค่คำว่า password
# ถ้าจับกว้างเกินจนคนเจอ false positive บ่อย สุดท้ายจะโดนปิดทิ้ง
scan() {
  local label="$1" re="$2"
  local hits
  hits=$(echo "$files" | xargs -I{} sh -c 'grep -HInE "$1" "{}" 2>/dev/null' _ "$re" \
    | grep -vE 'EXAMPLE|example|placeholder|PLACEHOLDER|changeme|CHANGEME|xxxx|<[A-Za-z_ ]+>|\$\{|\$\(|os\.Getenv|process\.env|secrets\.|vars\.')
  if [ -n "$hits" ]; then
    say "🔴 $label"; echo "$hits" | head -10 | cut -c1-160 | sed 's/^/    /'; fail=1
  fi
}

scan "private key ฝังอยู่ในไฟล์"        'BEGIN[A-Z ]*PRIVATE KEY'
scan "AWS access key"                    'AKIA[0-9A-Z]{16}'
scan "GitHub token"                      'gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,}'
scan "Google API key"                    'AIza[0-9A-Za-z_-]{33,}'
scan "Slack token"                       'xox[baprs]-[A-Za-z0-9-]{10,}'
# รูปแบบที่ทำให้หลุดจริง — ป้อนรหัสเป็นข้อความตรงๆ ให้ sudo หรือ mysql/psql
scan "รหัสผ่านเป็นข้อความตรงๆ ส่งให้คำสั่ง" "echo[[:space:]]+['\"][^'\"\$]{6,}['\"][[:space:]]*\|[[:space:]]*sudo[[:space:]]+-S"
scan "รหัสผ่านเป็นข้อความตรงๆ ใน flag"      "--(password|token)[= ]['\"]?[A-Za-z0-9@#%^&*_.+-]{8,}"

if [ "$fail" = "0" ]; then echo "✅ ไม่พบความลับใน repo"; fi
exit $fail
