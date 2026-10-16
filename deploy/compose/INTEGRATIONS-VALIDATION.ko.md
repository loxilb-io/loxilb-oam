# 선택 연동 검증 — 2026-10-16

[English](INTEGRATIONS-VALIDATION.md)

외부 Keycloak/SIEM 연동을 기본으로 유지하고 부가 서비스를 독립 Compose 프로젝트로
선택 배포하는 변경의 검증 기록이다. 원본 감사 로그·토큰·비밀번호·인증서 private key는
테스트베드의 보호된 증거 디렉터리에 남기며 이 문서에는 포함하지 않는다.

## Source and runtime scope

- OAM base: `f7b7525c2f2bc56536b6ff67ac666e1a5f242e38` (main과 일치 확인).
- Gateway contract: local `1d513937`, fetched main `40cdb72b8dbb57ec834436eac759a04939d5061b`와
  JWT/audit 핵심 계약 차이 없음 확인. 실행 이미지는 아래 별도 provenance로 구분한다.
- UI source: `6816f28d2985c63e956d6787cce916cd304198a9`의 JWT/Audit 경로·입력·connector를
  참고했다. 사용자 작업 중인 UI 변경은 수정하지 않았다.
- Linux x86_64 테스트베드, Compose v5.5.1. 기존 management/data/state 프로젝트를
  수정하지 않고, 별도 프로젝트·포트·새 CA·새 비밀번호·새 볼륨으로 clean replay했다.
- 최종 검증 Gateway는 `--proxyonlymode`, `privileged=false`, `CapDrop=[ALL]`,
  `no-new-privileges`로 실행했다. 운영 호스트 네트워크를 공유하지 않았다.

| Image | Validated digest |
|---|---|
| Keycloak 26.8.0 | `sha256:27b3230fbabb8c9ff3c66c864cf09dcd8a41aca82e5100679d109ef0eeb05741` |
| PostgreSQL 18.6 | `sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336` |
| syslog-ng 4.8.3 | `sha256:0956c0f2a94bc5ad4ff88e36ac1f3f59d5bae29c906555139f6633045f213ddf` |
| Gateway (testbed's existing image, reused in isolation) | image ID `sha256:ee119fcdb3665b13b02786b7348dce6c6eafc27f5e312269a13743ce83a1181b` |

부가 Compose와 `.env.example`은 위 부가 이미지 digest를 기본값으로 고정한다.
Gateway image ID를 source commit 또는 신규 release qualification으로 간주하지 않는다.

## Results

| 검증 | 결과 및 증거 수준 |
|---|---|
| 한영 발행 | 전체 영문·한국어 매뉴얼과 검증 보고서 제공. 명령/JSON 블록, digest, 절 구성, 로컬 링크 일치 확인 |
| 리허설 소스 일치 | PR의 Compose/설정 원본/준비·검증 스크립트 5개가 최종 Linux 리허설 파일과 바이트 단위로 일치 |
| PR 전 저장소 검사 | Go 1.26.7에서 gofmt, build, vet, make test, golangci-lint 2.12.2 통과. 저장소 설정 Gitleaks 및 Compose/Python 검사 통과 |
| 기본 배포 | `config --services`: postgres, oam-loxilb, ui-assets, caddy만 존재. 부가 서비스 자동 시작 없음 |
| 선택 배포 분리 | Keycloak/DB, syslog-ng가 각각 독립 프로젝트로 기동. loopback publication, 외부 named volume 사용 |
| Compose 계약 | Keycloak·SIEM 독립 파일 및 dataplane+trust overlay config 통과 |
| Seed custody | macOS 및 Linux에서 6개 unittest 통과. opt-in 필수, 잘못된 hostname 거부, 재실행/선택 추가 시 secret 보존, CA/인증서 쌍 오류 거부, 설정 변경 명시적 갱신 요구 |
| 엄격한 umask | umask 077에서 새 seed 생성. 공개 CA 디렉터리 0755, private 0700, 개별 read-only runtime 파일 0444. capability 없는 컨테이너에서 정상 읽기 |
| Keycloak TLS·발급 | CA 검증된 discovery, 실제 client_credentials 토큰 발급, 잘못된 client secret 401 |
| Gateway 관리 인증 | 전용 관리 토큰 조회 성공, 무인증 401. JWT 프로필 저장/재조회 일치 |
| 정상 추론 | 유효 JWT 200 및 backend 실제 수신 +1 |
| 거부 경로 | 누락·변조·서명 오류·만료·잘못된 issuer/audience 401, 모델 권한 없음 403. 각 거부의 backend 수신 증가 0 |
| 정상화 | 프로필 설정 복구 후 유효 JWT 200, backend +1 |
| IdP 장애·복구 | Keycloak pause 중 캐시 없는 **새** 프로필/임시 규칙은 503, backend 증가 0. unpause 후 같은 새 프로필에서 200/+1. 임시 규칙 삭제 확인 |
| 합성 TLS 수집 | RFC5425 octet-count framing 수신, RAWMSG와 원문 RFC5424/JSON 바이트 일치. 잘못된 CA/SAN 및 평문 거부 |
| 실제 Gateway 수집 | 관리 변경의 durable intent/result 쌍을 boot_id/seq로 대조. 활성 JSONL 및 압축 sealed segment 포함 |
| 실제 원문 무결성 | 최종 인수 시점 unique receiver 186건 / local 186건, 전건 JSON MSG 바이트·SHA-256 일치 |
| Gateway sink TLS 거부·복구 | 잘못된 server_name 및 issuer가 없는 CA bundle에서 TLS 실패 확인. 정상 설정 복구 후 연결 및 수신 재개 |
| 수집기 장애·복구 | 중단 중 신규 관리 변경의 intent/result가 로컬에 유지. 수집기 재시작 후 해당 backlog 수신 |
| 해제·재연결 | sink 해제 후 새 관리 변경이 수신기에 도착하지 않음. 재연결 후 신규 변경 수신 확인 |
| 저장소 보존 | Keycloak/DB 및 수집기 각각 down/up 후 컨테이너 ID 교체, realm/client secret/토큰 발급 및 기존 receipt 보존 |
| 리허설 정리 | 소유 라벨 확인 후 테스트 Gateway 설정/로그를 보호 보관하고 테스트 컨테이너·프로젝트만 제거. 모든 external volume·seed·증거 보존 |
| 기존 운영 서비스 | Caddy, OAM, Gateway, state PostgreSQL의 시작 시각은 모두 2026-10-14 그대로, 계속 running/OAM healthy |

감사 대조는 receiver를 먼저 읽고 local을 나중에 읽어 동시 기록 시점 차이를 피했다.
Gateway의 `submitted`나 socket write 성공을 수신/영속 저장 증거로 사용하지 않았다.

## Negative evidence and repairs

초기 실패 로그도 보호된 테스트 증거와 함께 보존했다.

- AI tenant 정책 DB가 없는 초기 환경에서 유효 JWT가 503으로 거부됐다. 테스트 전용
  정책 DB를 준비하고, 운영 가이드에도 이 의존성을 명시했다.
- 공개 CA 디렉터리 접근 제한으로 JWKS 검증이 실패했다. private 디렉터리 보호는
  유지하면서 공개 CA mount root의 읽기/검색 권한을 바로잡고 검증했다.
- 새 umask 077 환경에서 syslog-ng 설정 bind를 읽지 못했다. 보호된 0444 runtime
  copy를 준비하도록 고쳤고, 동일 조건의 실제 수집 및 unittest로 회귀 확인했다.
- 초기 레코드 비교에서 압축 sealed segment 누락으로 대조가 실패했다. 전체 원본을
  포함하고 관리 변경의 intent/result 쌍으로 검증 범위를 구체화했다.
- Gateway 관리 HTTP가 열리는 시점과 AI 정책 DB/keyset 준비 시점은 다르다. 초기
  준비는 최대 15초 내 503/무수신을 확인하고, 준비 후 200/+1을 별도로 검증했다.

## Repeatable repository checks

```bash
python3 deploy/compose/scripts/test_prepare_integrations.py

docker compose --env-file /dev/null \
  -f deploy/compose/docker-compose.keycloak.yml config --quiet
docker compose --env-file /dev/null \
  -f deploy/compose/docker-compose.siem.yml config --quiet

git diff --check
```

실제 배포·연결·거부·복구 리허설 순서는 [INTEGRATIONS.ko.md](INTEGRATIONS.ko.md)의
준비 → 독립 기동 → TLS/claim 확인 → Gateway 설정/readback → 추론/backend receipt →
감사 원본/receiver → 장애/복구 → down/up 보존 순서다. 테스트 전용 runtime harness와
개별 단계 로그는 보호된 테스트베드 증거 bundle에 보존했다.

## Qualification limits

- UI 경로/필드는 source 검토다. 이 변경에서 실제 브라우저 생성/거부/복구 또는
  스크린샷 검증을 수행했다고 주장하지 않는다.
- 전체 Linux eBPF/host-network dataplane, 성능, Kubernetes/HA, 외부 상용 SIEM의
  인덱싱·분석·경보, mTLS client 인증, 조직 CA 운영 및 release CI는 별도 범위다.
- 테스트 정책 DB는 격리된 리허설 PostgreSQL의 별도 DB였다. 운영 Gateway는 기존
  converged AI-key store 역할/schema를 사용하며, 테스트 DB owner 권한을 운영
  least-privilege 검증으로 간주하지 않는다.
- TLS syslog에는 application ACK가 없다. 이 리허설의 원문 일치와 backlog 복구를
  모든 장애에서의 무손실/중복 없음 보장으로 확장하지 않는다.
