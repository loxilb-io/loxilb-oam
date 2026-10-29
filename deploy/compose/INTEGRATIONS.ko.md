# 외부 Keycloak·SIEM 연동과 선택 배포

[English](INTEGRATIONS.md)

기본 구성은 운영자가 이미 보유한 Keycloak 및 SIEM을 사용한다. 기존
`init-converged.sh`와 관리/상태/데이터 Compose 명령에는 부가 서비스를 추가하지 않는다.
필요한 경우에만 아래 독립 프로젝트를 시작한다. Keycloak은 **추론 API의 Bearer
인증**을 위한 IdP이다. OAM 로그인 계정/JWT, OAM→Gateway 서비스 토큰은 별도이며,
이 구성으로 관리 콘솔의 SSO가 활성화되지는 않는다.

| 선택 | 실행 파일 | 저장소 | 기본 호스트 포트 |
|---|---|---|---|
| 외부 시스템 사용(기본) | 추가 실행 없음 | 외부 운영자 관리 | 외부 주소 |
| Keycloak만 | `docker-compose.keycloak.yml` | 전용 PostgreSQL 외부 볼륨 | `127.0.0.1:18443` HTTPS |
| 감사 수집기만 | `docker-compose.siem.yml` | 수신 로그·수집기 상태 외부 볼륨 | `127.0.0.1:16514` TLS syslog |
| 둘 다 | 두 파일을 **각각** 실행 | 위와 동일 | 위와 동일 |

syslog-ng는 SIEM으로 전달할 감사 로그를 수신·보관하는 수집기다. 검색 화면,
상관 분석, 경보 기능을 제공하는 완전한 SIEM 제품은 아니다. 외부 SIEM이 TLS syslog를
직접 받으면 이 수집기를 건너뛴다. HTTP HEC, Beats, 일반 TCP/UDP 입력은 Gateway의
TLS syslog sink와 직접 호환되지 않는다. 해당 제품의 RFC5424/RFC5425 입력 또는
별도 어댑터를 준비한다.

## 1. 외부 Keycloak 연결(기본)

1. IdP 운영자에게 realm issuer, discovery/JWKS URL, audience, tenant/model 권한
   claim 및 서버 인증서 체인을 받는다. 예:
   `https://id.example.internal/realms/inference`, audience `inference-api`.
2. Gateway 실행 환경에서 DNS·HTTPS 접근과 CA 신뢰를 확인한다. 브라우저에서
   열리는 것만으로 Gateway 연결이 확인되지는 않는다. `issuer`는 토큰 `iss`와
   포트/경로까지 정확히 일치해야 한다. `jwks_url` 생략 시 issuer의 OIDC discovery를
   사용한다. 외부 공개 CA이면 일반적으로 추가 trust mount가 필요 없다.
3. 사설 CA이면 **공개 CA 인증서만** 별도 디렉터리에 넣는다. 계획된 데이터 플레인
   유지보수 시 `docker-compose.integration-trust.yml`을 데이터 파일 뒤에 추가한다.
   `INTEGRATION_CA_DIR`을 해당 디렉터리로 지정하고 Gateway를 재생성한다. 이 단계는
   Gateway 재시작/트래픽 중단을 수반하므로 단순 OAM 업그레이드와 함께 실행하지 않는다.
   기존 추가 신뢰 경로가 있다면 overlay의 `SSL_CERT_DIR`에 함께 보존한다.
4. UI에서 인스턴스를 선택하고 **AI Gateway → JWT Auth Profiles**를 연다.
   관리자로 전체 프로필을 저장한 후 새로고침하여 값을 확인한다. 대표 API body:

   ```json
   {
     "name": "external-keycloak",
     "issuer": "https://id.example.internal/realms/inference",
     "jwks_url": "https://id.example.internal/realms/inference/protocol/openid-connect/certs",
     "audiences": ["inference-api"],
     "algs": ["RS256"],
     "tenant_claim": "tenant_id",
     "roles_claim": "realm_access.roles",
     "model_role_prefix": "model:",
     "model_authz": "claims-required"
   }
   ```

   쓰기: `POST /netlox/v1/config/ai/jwtauthprofile`, 확인:
   `GET /netlox/v1/config/ai/jwtauthprofile` (Gateway 관리 인증 필요).
   realm/client에 `tenant_id`, audience, `model:<모델명>` 역할이 실제 access token에
   포함되도록 mapper와 사용자/서비스 계정 권한을 설정한다. 빈 audiences는 audience
   검사를 생략하므로 운영 예제로 쓰지 않는다. 토큰·client secret을 로그에 출력하지 않는다.
5. **Traffic → LB Rule**에서 대상 AI 서비스의 `api_key_auth`를 `jwt`,
   `jwt_auth_profile`을 위 이름으로 설정한다. `apikey-or-jwt`는 API-key/Bearer 혼용이고,
   API key가 있으면 그 결과가 우선한다. `required`는 API-key 전용이다.
   저장 후 LB 규칙을 다시 읽는다. JWT의 tenant/user 정책 확인에도 Gateway의 AI-key/tenant PostgreSQL 저장소가 필요하므로 converged DB 연결과 schema `aigw`의 정상 상태를 먼저 확인한다. IdP와 프로필만 준비하고 이 저장소를 생략하면 유효 JWT도 503으로 거부될 수 있다. Bearer는 기본적으로 upstream으로 전달되지 않는다.
6. 유효 토큰 200 및 backend 실제 수신, 토큰 없음/만료/서명·issuer·audience 오류 401,
   모델 권한 없음 403 및 backend 수신 증가 0을 검증한다. JWKS 갱신 장애 동안 마지막
   사용 가능 키를 이용할 수 있으므로 IdP 중단 직후 전부 거절된다고 가정하지 않는다.
   키를 얻지 못한 새 프로필은 503으로 실패해야 한다.

브라우저 경로와 필드는 소스 기준이다. 배포 UI에 메뉴가 없으면 UI/Gateway 버전과
capability를 먼저 확인한다. 메뉴 노출은 실제 브라우저 검증과 별도로 기록한다.

JWT 프로필 화면의 주요 입력(영문 UI 기준):

| 화면 필드 | 필수/선택 | 대표 값·의미 |
|---|---|---|
| Name | 필수 | `external-keycloak`; LB 참조 이름, 최대 63바이트 |
| Issuer | 필수 | realm URL; 토큰 `iss`와 일치 |
| JWKS URL | 선택 | 공개 키 URL; 비우면 issuer discovery 사용 |
| Audiences | 선택이나 운영 시 명시 | `inference-api`; 여러 값은 쉼표 구분 |
| Signature algorithms | 선택 | `RS256`; realm 서명 알고리즘과 일치 |
| Model authorization | 선택 | 기본 `claims-required`; 모델 권한 없는 토큰 거부 |
| Tenant claim | 선택 | 기본 `tenant_id`; 실제 토큰 claim과 일치 |
| Roles claim / Model role prefix | 선택 | `realm_access.roles` / `model:` |
| Models claim | 선택 | 모델 배열 claim이 있으면 해당 dot-path; 없으면 비워 역할에서 유도 |
| Default tenant | 선택 | 기본 공란; tenant claim이 없는 토큰은 401 |

저장 후 목록과 `Keyset health`를 확인한다. 신규 프로필 또는 키 설정 변경 시 첫 JWKS fetch가 성공하기 전에는 잠시 503이 발생할 수 있다. 동일 설정을 재저장하는 것만으로 기존 사용 가능 키가 지워진다고 가정하지 않는다. 조회 목록에 행이 있다는
것만으로 키 준비 완료라고 판단하지 않는다.

## 2. 외부 SIEM 연결(기본)

1. SIEM 운영자에게 TLS syslog 주소(`siem.example.internal:6514`), 인증서 SAN,
   신뢰 CA, 필요하면 Gateway용 mTLS client cert/key를 받는다. 수신기는 RFC5424와
   RFC5425 octet-count framing을 지원해야 한다.
2. CA/client 파일은 **Gateway 컨테이너에서 읽을 수 있는 경로**에 준비한다. OAM
   서버 경로 또는 브라우저 PC 경로를 입력하지 않는다. 기존 Gateway config volume을
   사용하거나 검토한 read-only bind를 추가한다. CA private key는 넣지 않는다.
3. UI **Maintenance → Audit Trail → Compliance sink**에서 설정한다.
   관리자만 수정할 수 있다. operator는 조회, viewer는 해당 설정 화면 접근 제한이다.

   ```json
   {
     "enabled": true,
     "address": "siem.example.internal:6514",
     "ca_bundle_path": "/etc/loxilb/siem-ca.crt",
     "server_name": "siem.example.internal",
     "client_cert_path": "",
     "client_key_path": ""
   }
   ```

   쓰기/확인: `POST`/`GET /netlox/v1/audit/sink`. 쓰기 성공은 본문 없는 HTTP 204이고 조회 성공은 HTTP 200이다.
   mTLS일 때 client 인증서/키 경로를 모두 채운다. 검증 생략 옵션은 없다.
   named sink는 `/audit/sinks/{name}`를 사용하며 기관에 배정된 enterprise number가
   필요하다. 문서용 `32473`을 운영 번호로 복사하지 않는다.
4. `GET /audit/status`, sink readback, 새 감사 이벤트의 **수신 파일·레코드 ID·hash**를
   함께 확인한다. `connected`, 전송 카운터, TCP write 성공만으로 SIEM 보관을
   판정하지 않는다. 원본 Gateway JSONL과 수신 JSON MSG를 `boot_id`/`seq`(필요 시 `segment_uuid`)로 대조하고, 줄 끝 LF를 제외한 원문 바이트의 SHA-256이 같은지 확인한다. 레코드에 hash 필드가 있다고 가정하지 않는다.
5. 잘못된 CA/SAN 거부, 수신기 중단 중 로컬 기록 유지, 복구 후 backlog 수신,
   Gateway/수신기 재생성 후 보관, sink 해제 후 신규 전송 중단을 확인한다.
   TLS syslog는 application ACK가 없으므로 중복·재전송과 끝단 유실 가능성을 별도로
   관리한다. Gateway retention으로 이미 삭제된 구간은 자동 복구되지 않는다.

Compliance sink 화면에서는 최초 `Configure`, 변경 시 `Change`를 누르고 입력 후
`Save`, 이어서 `Refresh`로 재조회한다. `Stop`은 설정을 보관하는 일시 정지가 아니라
sink 설정 해제다.

| 화면 필드 | 필수/선택 | 선택 수집기의 대표 값·의미 |
|---|---|---|
| Receiver address (host:port) | 필수 | `127.0.0.1:16514`; Gateway 관점의 수신 주소 |
| CA bundle path | 필수 | `/run/integration-ca/integration-ca.crt`; Gateway 내부 경로 |
| Server name in the certificate | 선택 | `localhost`; 비우면 수신 주소의 호스트로 SAN 검증 |
| Client certificate path / Client key path | 선택(쌍) | mTLS일 때만 둘 다 설정 |
| Syslog facility | 선택 | 공란은 기본 13 |
| Largest message (bytes) | 선택 | 공란은 제한 없음; 작은 값은 shortening과 원문 비교에 영향 |

저장 후 `Sink the gateway holds now`의 값과 `Session established`, `Last error`를
확인한다. `Records written to the socket`은 수집기 영속 저장 건수가 아니므로 별도로
receiver 파일을 확인한다. `Keyset health`와 sink 상태는 서로 독립적이다.

## 3. 선택 배포 준비

Linux의 `deploy/compose`에서 실행한다. Docker Compose, Python 3, OpenSSL이 필요하다.
`.env.example`의 OPTIONAL integrations 변수는 기본 배포에 영향을 주지 않는다.
다른 포트나 볼륨 이름은 `.env`와 준비 명령의 환경에 **동일하게** 지정한다.
준비 스크립트는 `.env`를 직접 읽지 않으므로 custom volume은 아래처럼 명시한다.

```bash
# Keycloak만
python3 scripts/prepare-integrations.py --keycloak
# 또는 수집기만
python3 scripts/prepare-integrations.py --siem
# 또는 둘 다
python3 scripts/prepare-integrations.py --keycloak --siem

# custom example: .env의 KEYCLOAK_DB_VOLUME도 같은 이름으로 설정
KEYCLOAK_DB_VOLUME=my-keycloak-db python3 scripts/prepare-integrations.py --keycloak
```

공통 CA와 선택한 서비스의 인증서(90일), 무작위 초기 관리자/DB 비밀번호를
`integrations/private/`(0700)에 한 번 생성한다. 비밀번호와 TLS key 파일은
컨테이너 UID 1000이 개별 read-only mount를 읽도록 0444지만, 호스트의 상위 디렉터리는
0700이다. 상위 디렉터리를 공개하거나 전체 트리를 서비스에 마운트하지 않는다.
`integrations/trust/`에는 공개 CA만 둔다. 두 디렉터리는 Git에서 제외한다.
수집기 설정도 `integrations/syslog-ng.conf`에서 private 디렉터리의 0444 runtime copy로 준비하므로 엄격한 umask 077에서도 capability 없이 읽을 수 있다. 재실행은 값을 보존하며 설정/CA가 다르면 실패한다. 수집기 설정 변경은 원본을 검토한 뒤 `install -m 0444 integrations/syslog-ng.conf integrations/private/syslog-ng.conf`로 runtime copy를 명시적으로 갱신하고 해당 서비스만 재생성한다. 준비 작업은 서비스를 시작하거나
기존 OAM/Gateway 설정을 바꾸지 않는다. 일부 생성 중 실패했다면 protected backup과
대조하여 복구하고, 파일 삭제/자동 재발급으로 기존 DB 비밀번호를 교체하지 않는다.

기본 Keycloak URL은 `https://localhost:18443`이다. Gateway와 동일 호스트에서
사용하고 관리 PC는 `ssh -L 18443:127.0.0.1:18443 operator@oam.example.internal` 터널로 접근한다.
관리 PC에서 `https://localhost:18443/admin/`을 연다. 호스트/포트가 다르면 **처음 준비할 때**
`--keycloak-host id.example.internal --keycloak-port 18443`을 지정하고 DNS,
`KEYCLOAK_BIND_IP`, `KEYCLOAK_HTTPS_PORT`를 일치시킨다. 기본 포트를 다른 프로세스가
점유하면 임의 종료하지 말고 포트를 변경한다. 원격 Gateway는 이 호스트의 loopback을
사용할 수 없으므로 접근 가능한 주소/방화벽과 올바른 SAN이 필요하다.

자체 서명 CA는 평가/폐쇄망용이다. 운영 환경은 조직 CA/공인 인증서, 키 보관,
인증서 만료 모니터링, 백업 및 복구 정책을 적용한다. syslog 수신기는 기본적으로
client certificate를 요구하지 않으므로 loopback 밖에 공개할 때는 접근 제어와
`peer-verify(required-trusted)` 및 client CA 설정을 적용한다.

## 4. 시작 및 연결

```bash
# 각 프로젝트는 독립 실행. 한 명령에 -f 두 파일을 합치지 않는다.
docker compose -f docker-compose.keycloak.yml config --quiet
docker compose -f docker-compose.keycloak.yml up -d
docker compose -f docker-compose.keycloak.yml ps

docker compose -f docker-compose.siem.yml config --quiet
docker compose -f docker-compose.siem.yml up -d
docker compose -f docker-compose.siem.yml ps
```

Keycloak은 PostgreSQL의 health 이후 시작하지만 JVM 초기화까지 추가 시간이 필요하다.

```bash
# CA를 명시해 HTTPS를 확인한다. -k로 우회하지 않는다.
curl --fail --cacert integrations/trust/integration-ca.crt \
  https://localhost:18443/realms/master/.well-known/openid-configuration
# TLS syslog 인증서/SAN 확인 (종료까지 5초)
timeout 5 openssl s_client -connect 127.0.0.1:16514 -servername localhost \
  -CAfile integrations/trust/integration-ca.crt -verify_hostname localhost -verify_return_error </dev/null
```

초기 관리자 비밀번호는 권한 있는 호스트 터미널에서 `integrations/private/admin_password`를
읽어 사용한다. 화면 캡처/셸 인자/로그에 남기지 않는다. admin console에서 업무용 realm,
client, mapper, 사용자/서비스 계정, 모델 역할을 만든 후 §1대로 연결한다. 운영 앱은
Authorization Code+PKCE 또는 confidential service client를 쓰고 예제용 password grant를
운영 인증 흐름으로 채택하지 않는다. 초기 bootstrap 관리자 계정은 업무 관리자 생성과
로그인 검증 이후 폐기한다. seed 비밀번호 변경은 기존 DB의 계정 변경을 대신하지 않는다.

동일 호스트 Gateway의 Keycloak 프로필 issuer는
`https://localhost:18443/realms/<업무-realm>`이다. trust overlay의 공개 CA를 신뢰하도록
계획된 재생성을 거친다. 프로필/LB 규칙 생성은 자동화하지 않으므로 기존 정책을 덮어쓰지 않는다.

수집기 sink는 `address=127.0.0.1:16514`, `server_name=localhost`,
`ca_bundle_path=/run/integration-ca/integration-ca.crt`로 설정한다. trust overlay를
추가하지 않는 외부 Gateway라면 동일 CA를 별도 Gateway 경로에 배포하고 경로를 바꾼다.

```bash
# 실제 수신 기록 확인: 민감한 운영 로그이므로 공개 로그/PR에 복사하지 않는다.
docker compose -f docker-compose.siem.yml exec -T syslog-ng \
  sh -c 'ls -l /var/log/siem; wc -l /var/log/siem/receipts-*.log'
```

수신 파일은 UTC 일자별 생성되며 RFC5424 원문과 JSON MSG를 보관한다. Gateway 원본
JSONL은 계속 authoritative source다. 이 예제에는 자동 삭제 정책이 없으므로 디스크
사용량/수신 정체를 감시하고 조직 보존 기간에 따라 별도 archive/삭제 정책을 설정한다.
외부 SIEM으로 넘길 때는 해당 SIEM agent가 receipt 파일을 읽도록 전용 read-only
mount와 포맷/체크포인트를 설정하고 최종 SIEM 인덱스에서 동일 레코드 ID와 원문 SHA-256을 확인한다.
기본 수집기는 외부 제품에 자동 전달하지 않는다.

## 5. 업그레이드·중지·복구

- 기본 부가 이미지는 검증한 버전과 digest를 고정한다. 업그레이드 시 각 image 변수를
  승인된 `repo@sha256:...`로 변경하고 OAM/UI/Gateway·부가 이미지 다이제스트를 함께 기록한다.
- PostgreSQL은 전용 DB이며 기존 OAM/aigw DB를 공유하지 않는다. Keycloak DB는
  `pg_dump`/조직 백업 도구로 백업하고, private seed/인증서와 함께 접근 제한하여 보관한다.
- 업그레이드 전 DB dump, volume 이름, 이미지 digest, sink/profile readback, receiver
  마지막 레코드를 보관한다. DB/schema 업그레이드가 있는 Keycloak은 이미지 변경만으로
  downgrade가 보장되지 않으므로 구 버전 DB 복구 절차도 별도로 리허설한다.
- 중지: 각 파일로 `docker compose -f <해당파일> down`. external volume은 유지된다.
  `down -v`, prune 또는 volume 삭제를 업그레이드/복구에 사용하지 않는다.
- 수집기를 장기 중지하기 전 기존 sink 설정을 보호 백업하고 대상 sink를 해제/변경한다.
  compliance sink 해제는 `POST /audit/sink` body `{"enabled":false}`, named sink는
  `DELETE /audit/sinks/{name}`이다. 재연결 후 backfill/중복 및 retention gap을 확인한다.
- Keycloak 제거 전에 사용 중인 JWT 규칙을 계획적으로 변경한다. 규칙이 참조하는
  프로필은 삭제가 거부된다. IdP 중지가 기존 토큰의 즉각 취소를 의미하지 않는다.
- 인증서 갱신은 기존 CA/DB 비밀번호를 유지하며 서버 cert/key 쌍을 교체하고 해당
  서비스만 재생성한다. CA 변경은 신/구 CA를 함께 배포하는 전환 기간과 Gateway
  trust reload/재시작 검증을 포함한다. 준비 스크립트는 자동 갱신 도구가 아니다.

## 6. 인수 검증 범위

설정 정적 검사, 실제 TLS/토큰, Gateway readback, 추론 허용/거부와 backend receipt,
감사 원본/수신 hash, 장애/복구, 재생성/삭제/재연결을 구분하여 기록한다.
UI PASS는 실제 브라우저에서 생성·새로고침·거부·복구 동작을 확인한 경우에만 기록한다.
테스트 결과와 남은 제약은 [INTEGRATIONS-VALIDATION.ko.md](INTEGRATIONS-VALIDATION.ko.md)를 참조한다.

참고: [Keycloak container 운영](https://www.keycloak.org/server/containers),
[Keycloak hostname](https://www.keycloak.org/server/hostname),
[syslog-ng syslog source](https://syslog-ng.github.io/admin-guide/060_Sources/170_Syslog/000_syslog_source_options).
