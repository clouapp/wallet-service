# BlindPay, Bridge e Ozav no Macro

Implementado em `feat/admin-ramp-integrations`, sobre `origin/main` `42871a36`.
Frontend correspondente: `macro-wallets-front-ramps`, branch de mesmo nome.
A implementação está local; não houve deploy, migração de produção, cadastro
real de compliance ou movimentação de fundos.

## Domínio e origem

A própria empresa titular da conta faz KYB e opera como `account_holder`.
Seus clientes PF/PJ fazem compliance e operam independentemente como
`end_customer`. A API externa é consumida pelo servidor do cassino; o admin
consome os mesmos serviços com a sessão nativa.

O núcleo em `packages/rampengine` preserva os adapters, DTOs, validações,
serviços, repositórios, regras de lifecycle, recovery e testes da Xip. Ele não
registra bootstrap, autenticação, settings ou rotas da aplicação Xip. A camada
`app/services/ramps` compõe esse núcleo com os ports nativos; os repositórios
nativos mantêm o vínculo e a fila/outbox. O teste de arquitetura restringe o
componente copiado ao seu namespace e impede importação da aplicação nativa.

Fonte backend: `xip/backend` BlindPay `a31189c6286e819201f9ef17127b78c05fc3851b`
mais dev `4d34e49a17babc6ac8e5031be84482d3a7c7cd32`, combinados no snapshot
local de referência. A pilha BlindPay era uma branch de feature; não é tratada
como versão já publicada em produção. `packages/rampengine/origin.json` contém
hashes e adaptações de cada arquivo copiado. O inventário mais amplo da origem
permanece em `docs/ramp-integrations-source-manifest.json`.

As telas usam o comportamento dos apps Android `9ea843d82` e iOS `7b35db49f`
da Xip, adaptado ao admin web/HeroUI v3 e ao componente reutilizável do cassino.
O admin Xip dev `3809e55` e BlindPay `52586b8` serviram de referência operacional.

## Capacidades preservadas

- KYC PF: perfil, país/identidade fiscal, documentos, selfie, termos hospedados
  ou declarados, fan-out por provedor, pendências e reconciliação.
- KYB PJ Bridge: empresa, identidade fiscal, endereços, atividade/recursos,
  representante/UBOs/pessoas relacionadas, documentos, termos e ativação.
- Integrações externas, chaves Pix, livro de contas USD, beneficiários ACH/wire,
  registro de wallets empresariais e vínculos de destinos com provedores.
- Estimativa/reserva, roteamento, taxa/limites, validade, create/show/list,
  depósito por valor ou recorrente, conta virtual USD, payout address,
  funding por depósito ou ação preparada, renovação/finalização/cancelamento.
- Assinaturas de webhook, armazenamento/deduplicação, processamento assíncrono,
  reconciliação de ordens e contas, recuperação KYC/finalização, atenção
  operacional e remoção do material sensível depois da retenção.

**KYB completo existe no adapter Bridge da origem.** BlindPay e Ozav continuam
com as capacidades que seus adapters efetivamente implementam. O stub MutualPay
foi preservado sem anunciar uma integração funcional. Consultar capabilities
para cada titular; habilitar uma flag não cria uma capacidade de provedor.
A aceitação comercial de uma atividade pelo provedor permanece independente
da aprovação de identidade e das flags do Macro.

## Persistência, tenant e ambiente

A migração `00000000000720_create_ramp_integrations` instala atomicamente o
schema final do engine em `macro_ramps`, sem executar os backfills históricos
da Xip sobre o Macro. A conexão ORM `ramps` usa o mesmo Postgres, com esse
search_path. Os contatos do engine não têm autenticação nativa; o vínculo
público é `ramp_customers` com UUID, `account_id`, `external_id`, relacionamento
e tipo PF/PJ. E-mails iguais em tenants diferentes não compartilham cadastro.
Reservas de identidade fiscal/telefone também respeitam o tenant, com índices
e trigger no banco para impedir concorrência indevida.

`test` da conta resolve `sandbox`; `prod` resolve `production`. A escolha vem
da conta autenticada, nunca de um campo livre do navegador. Customers, workers,
recovery e callbacks conferem o ambiente antes de executar. Um evento assinado
em produção não atualiza um objeto de sandbox. Credenciais e políticas de rail
são independentes por ambiente; não há troca de modo global por requisição.

Tabelas nativas auxiliares: `ramp_customer_actors` (pseudônimo de auditoria),
`ramp_customer_features`, `ramp_tasks` e `ramp_outbox_events`.
A migração Down remove essas tabelas e o schema do engine; é destrutiva para os
dados de rampas e não é usada como procedimento de operação normal.

## API

Há duas superfícies explícitas em `routes/ramps.go`:

- `/v1`: sessão, conta selecionada, políticas de role e configuração TOTP.
- `/api/v1`: token de uma conta, throttle, assinatura HMAC quando exigida e
  permissões explícitas de rampas. Um token legado vazio não ganha acesso.

`GET/POST /customers`, `GET /account-holder`, `GET /customers/{customerId}`.
Todos os recursos abaixo ficam em `/customers/{customerId}` de cada superfície:

| Recurso | Rotas principais |
|---|---|
| Compliance | `GET /kyc`, `/kyc/overview`, `/kyc/profile`, `/kyc/business-profile`, `/kyc/capabilities`, `/kyc/countries` |
| Envio | `POST /kyc/application`, `/kyc/documents`, `/kyc/tos`, `/kyc/tos/acceptances`, `/kyc/overview/refresh` |
| Provedor | `GET /kyc/{provider}` e `/requirements`; `POST /application`, `/documents`, `/tos`, `/hosted-link`, `/refresh`, `/endorsements` |
| Vínculos | `GET /integrations`, `GET/POST /integrations/{provider}`, `POST /integrations/{provider}/refresh` |
| Destinos | livros `/pix-keys`, `/bank-accounts`, `/beneficiaries`; cadastro, leitura, alteração/exclusão previstos no router |
| Wallets | `GET/POST /wallets` |
| Ordens | `GET/POST /onramps`, `/offramps`; `POST /estimate`, `GET /{id}` |
| Funding | `POST /offramps/{id}/funding-action`, `/finalize`, `/cancel` |
| Contas | `GET/POST/PATCH /virtual-accounts`, `POST /payout-addresses` |
| Flags | `GET /features`; `PATCH /features/{feature}` somente sessão |
| Atenção | `GET /operations`, `POST /operations/{id}/resolve` somente sessão |

Simulação de depósito mantém a restrição sandbox da origem. Finalize exige
`Idempotency-Key`, ação/revisão vigente, assinatura e fingerprint correspondentes.
IDs de recursos filhos são sempre consultados no namespace do titular. Nunca
usar o ID de contato numérico do engine como identidade pública/autenticada.

Exemplo de criação PF, feito no servidor do integrador:

```json
{
  "external_id": "player-123",
  "relationship": "end_customer",
  "entity_type": "individual",
  "name": "Nome do jogador",
  "email": "jogador@example.com"
}
```

Para PJ acrescentar `representative_email`, `tax_id`, `tax_id_type` e
`incorporation_country` ISO alpha-3. A mesma referência/dados retorna o cadastro
existente; dados conflitantes retornam conflito. Há um titular PJ próprio por conta.

## Permissões, flags e settings

Grants: `ramp_customers.read/write`, `ramp_compliance.read`,
`ramp_compliance.profile.read`, `ramp_compliance.write`, `ramp_orders.read/create`.
Owner/admin operam. Auditor lê cadastro básico/status/ordens, sem perfil privado
ou movimentação. O acesso ao perfil é auditado também para tokens de API.
O admin controla flags de cliente com auditoria dentro da transação nativa.

| Flag | Default | Escopos |
|---|---|---|
| `ramp-integrations-enabled` | **false** | global e conta, ambos explicitamente habilitados |
| `ramp-payin-enabled`, `ramp-payout-enabled` | true | global/conta/override de cliente |
| `ramp-usd-payin-enabled`, `ramp-usd-payout-enabled` | true | global/conta/override de cliente |
| `ramp-bank-payout-enabled` | false | global/conta/override de cliente |
| `kyb` | false | global/conta/override de cliente |
| `kyc-usa-residence-enabled` | false | global |
| `ramp-own-operations-enabled` | true | global/conta |
| `ramp-customer-operations-enabled` | true | global/conta |

A flag principal é `ramp-integrations-enabled`, desligada por padrão. Exige ON
na plataforma **e** opt-in ON na conta. Protege cadastro, KYC/KYB, destinos,
consultas e operações no backend; o admin oculta os acessos e o Cloubet consulta
`GET /api/v1/ramps/status` pelo seu backend autenticado. Não basta esconder botões.
Os gates por produto e por cliente continuam valendo dentro desse controle.

Desativar a flag principal bloqueia o portal e novas assinaturas. Workers e
callbacks verificados continuam conciliando operações existentes. Consultas do
portal voltam quando a flag for reativada; o status da flag segue disponível.
Desativar um provedor também preserva a reconciliação dos seus recursos existentes.

Os grupos da origem foram registrados integralmente no settings nativo, seção
`ramps`, blocos `sandbox` e `production`. Nome:
`ramp_<grupo_original>_<ambiente>`, por exemplo
`ramp_provider_bridge_sandbox` e `ramp_rail_pix_payout_production`.
O catálogo completo de grupos/chaves/tipos/defaults/secrets/opções é
[`ramp-settings-catalog.json`](ramp-settings-catalog.json); contém somente
metadados e defaults de código, nunca valores configurados.

Estão incluídos credenciais, PEM RSA Bridge, HMAC Ozav/BlindPay, instance,
mints/decimais, delegate/RPC e confirmação de payouts Solana em produção da
BlindPay; ordem dos provedores, taxas, mínimos/máximos, overrides por token,
funding window, tolerância e tentativas Ozav. PEM é validado, secrets continuam
no armazenamento selado nativo e a atualização usa as permissões de provedores
da plataforma Macro. Nenhuma credencial foi copiada. Provedores começam
**desabilitados**; cadastrar valores por ambiente antes de habilitá-los.

## Workers, callbacks e eventos

Callback dos provedores:
`POST /v1/webhooks/ramps/{sandbox|production}/{bridge|ozav|blindpay}`.
A verificação usa o corpo original e as credenciais daquele ambiente. Eventos
são persistidos antes do 202, identificados por modo/provedor/ID e processados
pela fila durável. Configure essa URL no painel de cada provedor.

`ramps:work` executa um ciclo. O processo local inicia o worker junto de
`localworkers`; o modo Lambda é `ramp_worker`, agendado a cada minuto no SAM.
Nada foi implantado. A fila usa claim `SKIP LOCKED`, lease, backoff e 12 tentativas;
os payloads carregam IDs e modo, sem documento/passphrase/material MPC. A fila
de atenção permite resolver resultado externo incerto ou registrar atendimento
com TOTP fresco, conferência do titular e auditoria.

Os nove comandos da origem foram mapeados: `kyc:reconcile`,
`kyc:recover-intents`, `ramps:reconcile`, `ramps:sync-virtual-accounts` e
`ramps:recover-finalizations` executam no ciclo de recuperação/reconciliação;
`kyc:resolve-intent` corresponde à resolução auditada no admin;
`kyc:scrub-intents` roda na retenção do worker; `ramps:prune-timeline` está
registrado e também roda com janela de 90 dias; `ramp:blindpay-check` está
registrado com `--environment=sandbox|production` e faz somente a consulta
autenticada de instância do adapter original. Esse diagnóstico não foi chamado
contra um provedor real. Os comandos nativos recebem serviços pela composição.

`KYC_INTENT_MATERIAL_RETENTION_HOURS` tem default 720; os limites e scrubber da
origem continuam em vigor. `RAMP_BRIDGE_BASE_URL`, `RAMP_OZAV_BASE_URL` e
`RAMP_BLINDPAY_BASE_URL` substituem endpoints somente em `local/testing`, para
stubs. Não se aplicam em produção.

A outbox encaminha notificações de compliance/ordens aos webhooks nativos
**da conta**, incluindo UUID público do cliente, `external_id` e versão 1.
Inscrições legadas sem conta ou de outra conta não recebem eventos de rampas.
A entrega usa a deduplicação, retry e assinatura já existentes no Macro.

## Funding e cassino

`POST /v1/customers/{id}/offramps/{orderId}/wallets/{walletId}/fund` usa somente destino/valor ou
ação já armazenados. Exige wallet ativa da conta, chain do mesmo ambiente,
endereço de origem, TOTP fresco e passphrase. Depósito usa o pipeline nativo de
withdrawal com idempotência da ordem. Ação preparada usa a custódia Solana,
assina a mensagem exata e finaliza pelo engine; o endpoint não aceita bytes de
transação arbitrários, chaves ou fingerprint fornecido pelo navegador.

A resposta `funding_not_submitted` identifica recusas anteriores ao envio;
timeouts e resultados desconhecidos não recebem esse código. Em
`GET /api/v1/customers/{id}/offramps/{orderId}/funding-evidence`, o integrador pode
confirmar uma recusa terminal explícita de autorização preparada. Para funding
por depósito, `?wallet_id=<UUID>` permite comprovar que uma ordem terminal não
gerou intenção de envio em nenhum dos dois ledgers nativos. A consulta valida a
wallet e o endereço de origem e compartilha a trava por ordem com o funding.
Qualquer registro nativo ou autorização desconhecida impede essa prova; uma
falha genérica ou a ausência de hash, isoladamente, nunca autoriza estorno.
O Cloubet mantém provas de broadcast, recebe os eventos nativos de funding por
depósito e concilia devoluções inclusive após a conclusão do saque. As operações
concluídas entram na verificação periódica por 90 dias e continuam aceitando
callbacks assinados de devolução posteriormente.

O mesmo funding está disponível em
`POST /api/v1/customers/{id}/offramps/{orderId}/wallets/{walletId}/fund` para o
servidor integrador, com grants explícitos `ramp_orders.create` e
`withdrawals.create`, wallet autorizada para o token, passphrase e assinatura
HMAC quando exigida. Tokens API usam o limite diário USD nativo; a ação preparada
reserva orçamento por ordem/revisão de forma idempotente. Não recebem um signer
genérico. A API de sessão continua exigindo TOTP fresco do operador.

O cassino identificado é `clou-bet/front` / `clouapp/back`, ambos baseados em
`origin/dev`. Os worktrees `cloubet-front-ramps` e `cloubet-back-ramps` contêm a
integração concreta. O front reutiliza a jornada visual, com HeroUI v2, tema do
cassino e traduções en/pt/es. A aba da carteira é carregada apenas com a flag
habilitada. O Next proxy encaminha a sessão Sanctum para `/api/macro-cash/*` no
backend correto do tenant; credenciais Macro ficam nas settings de custódia do PHP.

O backend Cloubet vincula `cloubet:player:{id}` ao customer Macro, escolhe os
endereços Solana do jogador e da custódia e mantém a referência por cliente de
API. Onramp envia para o endereço de depósito do jogador; o pipeline de depósitos
de custódia existente credita o saldo após confirmação da transferência.

Offramp usa **saldo do cassino**: cotação selada no servidor, validações existentes
(saldo, mínimo, locks, rollover, e-mail, 2FA e atividade), reserva decimal sob lock,
análise de risco e ledger idempotente. Casos suspeitos aguardam a aprovação
existente. O job guarda somente o UUID local e financia a ordem armazenada pela
API MPC. Resultado incerto mantém a reserva; callback assinado ou reconciliação
conclui e um refund comprovado estorna uma única vez. Não existe estorno manual
após funding incerto sem evidência do provedor. Tokens de outra conta e jogadores
diferentes não escolhem customer, wallet, valor reservado ou destino.

No Cloubet, endereços USD de payout com funding externo ficam indisponíveis;
saques ACH/wire usam ordens cotadas financiadas pelo saldo. Os fluxos diretos
originais continuam disponíveis no admin/API Macro quando o titular e o provedor
suportam essa capacidade. Exemplos genéricos em `examples/casino` continuam como
referência para outros integradores; Cloubet usa o ledger real do seu backend.

## Validação e ativação

Foram executados testes Go dos adapters/serviços portados e do backend nativo,
contratos de settings/permissões/webhooks, arquitetura em modo `ratchet`, teste
HTTP com Postgres descartável para tenant/ambiente/identidade/KYB, assinatura MPC
com nó falso e recusa de mensagem/adressamento/passphrase incorretos. O frontend
passa por typecheck, lint, Vitest, build e Playwright hermético desktop/mobile.

O banco de QA usa containers próprios `macro-ramps-qa-postgres`/`redis`; nenhuma
fixture foi executada no banco compartilhado. Para reproduzir o teste HTTP,
preparar Postgres descartável e executar:

```sh
RAMP_TEST_DB_HOST=127.0.0.1 RAMP_TEST_DB_PORT=26432 go test ./tests/feature/api/ramps -count=1
ARCH_MODE=ratchet go test ./tests/architecture/...
```

Antes de merge/deploy com movimentação, executar o e2e de testnet exigido pelo
repositório para alterações em custódia, com credenciais e fundos de teste do
ambiente. Essa validação externa não foi executada; o teste local usa stubs.
A ativação depende da migração 720 do Macro e da migration
`2026_11_02_220000_create_macro_ramp_operations` do Cloubet, settings e secrets por
ambiente, hot wallet Solana de saque, endereços individuais de depósito, grants
API, orçamento, webhook de custódia com os eventos `ramp.order.*`, workers e
`macro-ramps:reconcile` do scheduler PHP. Habilitar provedores/rails compatíveis e,
por último, a flag principal na plataforma e na conta. Nenhuma flag foi ativada.

Validação local adicional: 1.067 testes do admin; 8 cenários Playwright
herméticos desktop/mobile; typecheck/lint/build; 32 testes PHP dos novos fluxos,
assinatura, gates e isolamento; testes de UI/transporte do Cloubet. Os resultados
reproduzíveis do cassino estão no runbook `docs/macro-ramps.md` de seus repositórios.
