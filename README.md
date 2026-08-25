# vertex-bff

ชั้นกลางระหว่างหน้าบ้าน (iOS app, back office) กับ microservice ของ Vertex

## ทำไมต้องมี

ทั้ง iOS และ back office ต้องประกอบข้อมูลข้าม service เองที่ฝั่ง client เช่นหน้า Pets
ของ back office ดึงตาราง user ทั้งระบบมาเพื่อแปลง id ของผู้ดูแลเป็นชื่อไม่กี่ชื่อ
และหน้า Home ของแอปโหลดรูปแมวทุกตัวเพื่อแสดงแค่จำนวนกับชื่อหนึ่งชื่อ

BFF ย้ายการประกอบข้อมูลแบบนี้มาไว้ฝั่ง server เพื่อให้หน้าจอยิงรอบเดียวจบ

## สถานะ

| ส่วน | สถานะ |
|---|---|
| GraphQL schema | ✅ ([VT-98](https://thepphithakp.atlassian.net/browse/VT-98)) |
| resolver + กัน N+1 | ✅ ([VT-101](https://thepphithakp.atlassian.net/browse/VT-101)) |
| deploy ด้วย Helm | ✅ chart อยู่ที่ `helm/vertex-bff` ทำผ่าน GitHub Actions |
| ย้าย client มาใช้ | ยังไม่เริ่ม — back office [VT-79](https://thepphithakp.atlassian.net/browse/VT-79)/[VT-80](https://thepphithakp.atlassian.net/browse/VT-80)/[VT-81](https://thepphithakp.atlassian.net/browse/VT-81), iOS [VT-102](https://thepphithakp.atlassian.net/browse/VT-102) |

proxy REST ชุดเดิมถูกเอาออกแล้ว — ไม่มีใครเรียกและ path ไม่ตรงกับที่ client ใช้จริง
ตอนนี้ service นี้มี route เดียวคือ `POST /graphql` (บวก `/livez` `/readyz` `/health`)

## โครงสร้าง

```
main.go                     ประกอบ server, middleware, guardrail
graphql/schema.graphql      GraphQL schema
graphql/operations.graphql  query ของทุกหน้าจอ ใช้เป็นทั้งสเปกและชุดตรวจ schema
graphql_test.go             test ที่รันจริงกับ upstream ปลอม
internal/client/            REST client ของแต่ละ service
internal/loader/            กัน N+1 ด้วยการดึงครั้งเดียวต่อ request
internal/graph/             resolver + การแปลง DTO เป็น model
helm/vertex-bff/            chart สำหรับ deploy
```

## รันในเครื่อง

```sh
PUBLIC_BASE_URL=http://localhost:3000 \
PET_SERVICE_URL=http://localhost:8081 \
AUTH_SERVICE_URL=http://localhost:4000 \
EVENT_SERVICE_URL=http://localhost:4002 \
ENABLE_INTROSPECTION=true \
go run .
```

`PUBLIC_BASE_URL` ไม่ตั้ง = service ปฏิเสธที่จะ start โดยตั้งใจ เพราะถ้าปล่อยผ่าน
`avatarUrl` จะชี้ไปที่อยู่ภายในคลัสเตอร์ที่ client เรียกไม่ถึง แล้วรูปจะหายทั้งแอป
โดยไม่มี error ให้เห็น

## หลักการที่ต้องรักษา

**resolver เรียก REST ของ service ไม่ยิง database ตรง** เพื่อให้การตรวจสิทธิ์ยังอยู่ที่ชั้น
service ตามที่ pet-service ตั้งใจไว้ และต้องส่ง JWT ของผู้เรียกต่อไปให้ service ปลายทาง
ไม่ใช่ให้ BFF ใช้สิทธิ์ของตัวเอง มิฉะนั้น BFF จะกลายเป็นช่องข้ามสิทธิ์

**ไม่เอา binary เข้า GraphQL** รูป avatar ยังเป็น REST ที่มี ETag อยู่แล้ว schema ให้แค่ URL

**ทุก operation ต้องมีชื่อ** เพราะชื่อจะถูกเขียนลง field `endpoint` ใน log
ถ้าปล่อยให้ยิง anonymous query ได้ จะกรองใน Kibana ไม่ได้ว่า request มาจากหน้าไหน

## ทดสอบ

```sh
go test ./...
```

test รัน GraphQL server จริงกับ upstream ปลอม และตรวจสิ่งที่พังแล้วเจ็บ:
ส่ง JWT ต่อไหม, ยิงไป auth-service กี่ครั้งเมื่อมีผู้ดูแลหลายคน (ต้อง 1),
รูปเป็น URL ไม่ใช่ base64, และ event-service ล่มแล้วการ์ดอื่นบน Dashboard
ยังแสดงได้ไหม

## ตรวจ schema

```sh
python3 -m pip install --user graphql-core
python3 - <<'PY'
from graphql import build_schema, parse, validate
s = build_schema(open('graphql/schema.graphql').read())
d = parse(open('graphql/operations.graphql').read())
errs = validate(s, d)
print("ok" if not errs else errs)
PY
```

## ตัวแปรสภาพแวดล้อม

| ชื่อ | ค่าเริ่มต้น | ใช้ทำอะไร |
|---|---|---|
| `PORT` | `3000` | พอร์ตที่ฟัง |
| `PET_SERVICE_URL` | `http://localhost:8081` | ปลายทาง pet-service |
| `AUTH_SERVICE_URL` | `http://localhost:4000` | ปลายทาง auth-service |
| `EVENT_SERVICE_URL` | `http://localhost:4002` | ปลายทาง event-service |
| `PUBLIC_BASE_URL` | **ไม่มี — บังคับ** | ที่อยู่สาธารณะ ใช้ประกอบ `avatarUrl` |
| `MAX_QUERY_COMPLEXITY` | `1000` | เพดานความแพงของ query |
| `MAX_QUERY_DEPTH` | `12` | เพดานความลึกของ query |
| `ENABLE_INTROSPECTION` | `false` | เปิดเฉพาะตอน dev |
| `UPSTREAM_TIMEOUT` | `10s` | timeout ตอนเรียก service |

บน k8s ให้ชี้ไปที่ ClusterIP ภายใน **ห้าม hardcode hostname จริงลงในไฟล์ที่ commit**
เพราะ repo นี้เป็น public — `PUBLIC_BASE_URL` กับ `ingress.host` ส่งตอน deploy
ผ่าน GitHub secret เท่านั้น

## Deploy

GitHub Actions ทำให้อัตโนมัติเมื่อ push ขึ้น `main` — secret ที่ต้องตั้ง:

| Secret | ใช้ทำอะไร |
|---|---|
| `KUBECONFIG_CONTENT` | เข้าถึงคลัสเตอร์ |
| `INGRESS_HOST` | host ของ ingress |
| `PUBLIC_BASE_URL` | ที่อยู่สาธารณะสำหรับ `avatarUrl` |

chart จะ**ปฏิเสธที่จะ render** ถ้าไม่ได้ส่ง `publicBaseURL` หรือ `ingress.host`
ดีกว่าปล่อยให้ deploy ค่าผิดขึ้นไปแล้วมาพังเงียบๆ ทีหลัง
