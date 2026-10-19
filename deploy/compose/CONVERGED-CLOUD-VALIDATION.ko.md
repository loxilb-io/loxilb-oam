# Cloud 자동 설치 검증 — 2026-10-19

[English](CONVERGED-CLOUD-VALIDATION.md) · [운영 가이드](CONVERGED-CLOUD.ko.md)

## 검증 환경과 범위

지정된 Ubuntu 24.04.5 LTS 클라우드 테스트베드에서 실제 사설 NIC와 공인 Floating IP로
검증했습니다. 기존 OS에서 애플리케이션을 신규 설치하고 원래 상태로 복구한 리허설이며,
VM OS를 재설치하지는 않았습니다. Gateway는 표준 privileged/host-network Compose
구성으로 실행했습니다. 추론 트래픽이나 eBPF 전달 성능을 검증한 것은 아닙니다.

기준 소스는 `46acdf7ffb74f205161674b6ebc23a008eb1d08f`와 이번 변경입니다.
Linux Python 3.12.3, OpenSSL 3.0.13, Docker Engine 29.1.3, Docker Compose
2.40.3 환경입니다. 기존 이미지를 재사용했으며 다시 빌드하거나 업그레이드하지 않았습니다.

| 구성 요소 | 로컬 이미지 ID |
|---|---|
| Gateway | `sha256:49d6581c4a5c2e62ad97b25ef7f8591efb44bcd3737efb5789225805de8a8a33` |
| OAM | `sha256:3bfbdcc49470fb9ee3cea808be4323bbf18e83e32fea94f87e587a723fa64ead` |
| Caddy | `sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f` |
| PostgreSQL | `sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336` |

이 ID는 검증한 런타임을 식별하며 소스-이미지 대응 증명이나 일반적인 릴리스/보안 인증을
의미하지 않습니다. 실제 브라우저 화면의 UI 버전은 v0.9.8.9였습니다.

## 결과

| 항목 | 결과 및 범위 |
|---|---|
| 기존 실패 | OAM의 `GW_HOST`가 없는 기본 브리지 주소를 가리켜 실제 OAM 경로는 시간 초과, 호스트 loopback은 정상. RED 증거 보존. |
| 자동 복구 | 초기화 스크립트가 실제 NIC를 선택하고 `GW_HOST`를 유지. 자체 서명 인증서·외부 인증서 볼륨을 갱신하고 관리 컨테이너 재생성. 수동 Compose 수정 없이 종료 코드 0. |
| 실제 OAM 경로 | 마운트된 공개 CA·호스트명·서비스 토큰으로 검증. 익명 LB 관리 401, 인증된 버전·포트 조회 200. `-k` 미사용. |
| Floating IP 주소 | site, 허용 origin, 인증서 IP SAN, SNI fallback 반영. 모든 설정 주소의 UI/OAM-health가 로컬 TLS 검증을 유지한 상태에서 200. |
| 외부 인바운드 | VM/NAT 외부 PC에서 Floating IP UI에 접속. 공개 인증서를 사용해 HTTP 200, TLS 검증 결과 0. |
| 신규 설치 | 별도 빈 PostgreSQL·config·audit·token·TLS 볼륨, 기존 `.env`·인증서 없는 상태에서 기본 선택과 공인 주소 입력으로 완료. 종료 코드 0, `local-gateway` 1개 자동 등록. |
| 원래 상태 복구 | 신규 프로젝트 3개를 `-v` 없이 중지한 뒤 원래 초기화 스크립트 종료 코드 0. 사용자 비밀번호 해시·인스턴스 행 및 인증서·비밀값 seed 바이트가 리허설 전 증거와 일치. 기존/테스트 볼륨 모두 보존. |
| 재실행 | 재발급 없이 초기화를 반복해 `.env`, edge 인증서/키, 서비스/DB 비밀 파일 3개의 바이트가 동일함을 확인. |
| 잘못된 입력 | scheme을 포함한 공인 주소를 비정상 종료 코드로 거부. `.env`, 인증서/키, 비밀값 바이트 보존 확인. |
| 실제 브라우저 | 기존 로그인된 Chrome에서 Instances → Check Health 실행. `local-gateway` **Healthy / Active** 확인. Dashboard의 시스템·연결 데이터 조회 정상. 사용자 비밀번호 초기화 없음. |
| 회귀 검사 | macOS/Ubuntu에서 Python 30개 통과. 잘못된 주소, 브리지 부재, 공인 SAN 거부/복구, CA/만료 인증서 분류, namespace/CA/token 사용, 익명 허용·시간 초과·edge TLS 실패 거부 포함. |
| 정적/비밀값 검사 | Bash 문법, ShellCheck warning 수준, 한·영 명령 일치/로컬 링크, diff 공백, 저장소 설정을 적용한 Gitleaks 디렉터리 검사 통과. |

## 보존한 실패와 한계

첫 Linux 회귀 검사에서 OpenSSL 동작 차이를 발견했습니다. Ubuntu 3.0의
`x509 -checkip/-checkhost`는 이름 불일치가 종료 코드 실패로 일관되게 반영되지
않았습니다. `openssl verify`로 변경했고 동일한 실패 fixture가 Ubuntu 3.0과
macOS 3.6에서 정상적으로 거부되는 것을 확인했습니다. 실패 로그는 보존했습니다.

호스트 내부에서 검사하는 것은 접속 주소·인증서이며 클라우드 방화벽 정책이나
Floating IP 연결을 인증하는 것은 아닙니다. 외부 인바운드는 별도 PC 검증으로
확인했습니다. 공인 주소는 운영자가 제공하며 외부 IP 조회나 클라우드 API 변경을
사용하지 않았습니다.

미검증: 신규 OS 프로비저닝/재설치, 다중 NIC 런타임/장애 전환, IPv6, 실제 CA/ACME
갱신, 브라우저 실패·복구 캠페인, 추론 정확성/성능, eBPF 전달, 외부 IdP/SIEM 전체
연동, HA/Kubernetes, 릴리스 적합성. CI 결과는 PR 검사에서 별도로 확인해야 하며
로컬 검사를 CI 통과로 간주하지 않습니다. 추가한 Ubuntu 24.04 CI job은 Python
설치/seed 회귀 검사를 수행하며 실제 클라우드 리허설을 실행하지 않습니다.

원본 로그, DB 백업, seed 백업, 브라우저 스크린샷은 비공개로 보관했습니다. 비밀번호,
개인 키, 실제 `.env`, DB dump, 클라우드 IP, 내부 호스트명, 원본 런타임 로그는
이 공개 보고서에 포함하지 않았습니다.
