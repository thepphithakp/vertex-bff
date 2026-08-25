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
| proxy REST (`/api/pets`, `/api/master-data`, `/api/auth`) | มีโค้ดแล้ว แต่ยังไม่ได้ deploy และยังไม่มีใครเรียก |
| GraphQL schema | ออกแบบแล้ว ([VT-98](https://thepphithakp.atlassian.net/browse/VT-98)) ยังไม่ได้ implement |
| resolver + dataloader | ยังไม่เริ่ม ([VT-101](https://thepphithakp.atlassian.net/browse/VT-101)) |

⚠️ path ที่ proxy รองรับตอนนี้ (`/api/pets`) **ไม่ตรง** กับที่ consumer เรียกจริง (`/api/v1/pets`)
และไม่มี route ของ event-service — ต้องแก้ตอนทำ resolver

## โครงสร้าง

```
main.go                    proxy ปัจจุบัน (Fiber)
graphql/schema.graphql     GraphQL schema
graphql/operations.graphql query ของทุกหน้าจอ ใช้เป็นทั้งสเปกและชุดตรวจ schema
```

## หลักการที่ต้องรักษา

**resolver เรียก REST ของ service ไม่ยิง database ตรง** เพื่อให้การตรวจสิทธิ์ยังอยู่ที่ชั้น
service ตามที่ pet-service ตั้งใจไว้ และต้องส่ง JWT ของผู้เรียกต่อไปให้ service ปลายทาง
ไม่ใช่ให้ BFF ใช้สิทธิ์ของตัวเอง มิฉะนั้น BFF จะกลายเป็นช่องข้ามสิทธิ์

**ไม่เอา binary เข้า GraphQL** รูป avatar ยังเป็น REST ที่มี ETag อยู่แล้ว schema ให้แค่ URL

**ทุก operation ต้องมีชื่อ** เพราะชื่อจะถูกเขียนลง field `endpoint` ใน log
ถ้าปล่อยให้ยิง anonymous query ได้ จะกรองใน Kibana ไม่ได้ว่า request มาจากหน้าไหน

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

บน k8s ให้ชี้ไปที่ ClusterIP ภายใน ห้าม hardcode hostname จริงลงในไฟล์ที่ commit
