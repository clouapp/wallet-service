# Integrações de rampas para tenants e clientes do Macro Wallets

Data: 2026-11-02. Este documento registra o desenho original. A implementação
local está em `feat/admin-ramp-integrations`; os nomes/contratos finais e a
validação estão no [runbook implementado](../../RAMP_INTEGRATIONS.md). Propostas
abaixo devem ser lidas como histórico, não como catálogo da API atual.

## Escopo definido com o produto

O Macro deve atender os dois casos, utilizando BlindPay, Bridge e Ozav:

1. A empresa titular de uma conta Macro faz seu KYB e opera em nome próprio.
2. Um cliente final dessa empresa, PF ou PJ, faz seu KYC ou KYB e opera em nome
   próprio. No exemplo do cassino, a experiência fica na interface do cassino,
   integrada à API do Macro.

Onramp e offramp continuam no escopo. O admin do Macro permite iniciar,
acompanhar e operar os fluxos, respeitando as permissões da conta. A API
externa disponibiliza os mesmos casos de uso para a integração da empresa.

O KYB da empresa, o KYC de seus clientes e a habilitação da empresa para um
produto no provedor são condições diferentes. Nenhuma aprovação é herdada
automaticamente por outro titular ou por outro provedor.

## Acesso e referências de código

| Repositório | Referência consultada | Observação |
|---|---|---|
| `xip/backend` | `origin/dev`, `4d34e49a17babc6ac8e5031be84482d3a7c7cd32` | Inclui KYB, USD Bridge e a correção de webhooks de transferências Ozav de 02/11 |
| `xip/backend` | `origin/feat/blindpay-7-pix-payout`, `a31189c6286e819201f9ef17127b78c05fc3851b` | Contém a pilha de desenvolvimento BlindPay; sete PRs ainda abertos na consulta |
| `xipcash/admin` | `origin/dev`, `3809e559c1aeb5aab1812fe22c0c4bdc37b00353` | Cliente 360, KYB, settings, flags e operação Bridge/Ozav |
| `xipcash/admin` | `origin/feat/blindpay-admin-1`, `52586b84396e472438b505643a03e10ecaeaa4aa` | Complemento BlindPay ainda em PR aberto |
| `wallet-service` | `origin/main`, `42871a36` | Base do worktree `wallet-service-ramps` |
| `macro-wallets-front` | `origin/main`, `9fe65ef` | Base do worktree `macro-wallets-front-ramps` |

O checkout `xipcash/backend` está mais antigo que `xip/backend`; não deve ser
usado sozinho como origem da migração. Os checkouts principais do Macro
também estão antigos e contêm alterações locais. O levantamento usa novos
worktrees, sem sobrescrever essas alterações.

O snapshot local de backend em `artifacts/xip-blindpay-ramps-reference` combina
`a31189c6` com `4d34e49a` por merge local sem commit. Ele é uma referência
composta, não um commit publicado do Xip. Os hashes dos arquivos consultados
ficam no manifesto de fontes para tornar essa composição verificável.

Dependências BlindPay confirmadas no GitHub:

- [120 — contrato e fundação](https://github.com/xipcash/backend/pull/120).
- [126 — KYC](https://github.com/xipcash/backend/pull/126).
- [127 — beneficiários](https://github.com/xipcash/backend/pull/127).
- [128 — KYC Pix e onramp](https://github.com/xipcash/backend/pull/128).
- [132 — cotação e preparação de funding](https://github.com/xipcash/backend/pull/132).
- [135 — finalização, recuperação e operação](https://github.com/xipcash/backend/pull/135).
- [136 — payout Pix](https://github.com/xipcash/backend/pull/136).
- [56 do admin — settings e compliance BlindPay](https://github.com/xipcash/admin/pull/56).

## Paridade real por provedor

Esta tabela descreve o código consultado do Xip, e não todo o catálogo comercial
de cada provedor.

| Capacidade | Bridge | Ozav | BlindPay na pilha de PRs |
|---|---|---|---|
| KYC PF, termos e documentos | Implementado; API e fluxo hospedado | Implementado; termos, uploads e verificação | Implementado; criação atômica após uploads e termos |
| KYB PJ, representantes e beneficiários finais | Implementado pelo adapter empresarial | Adapter empresarial ausente | Adapter empresarial ausente |
| Pix onramp | Contas virtuais e eventos de depósito | Conversão e entrega em duas fases | Cotação, wallet registrada e payin |
| Pix offramp | Transferência e destino Pix | Funding de wallet gerenciada, depois liquidação | Preparação, assinatura e finalização |
| USD | Conta virtual, ACH/wire e liquidation addresses | Não implementado nesse adapter | ACH/wire pelas novas portas de payout |
| Tokens e rede exercitados no código | USDC/USDT em Solana | USDC/USDT/BRS em Solana | USDC/USDT em Solana |
| Webhook | Verificação RSA | Verificação HMAC | Verificação Svix |

KYB Ozav e BlindPay são desenvolvimento adicional necessário para oferecer
empresas nesses provedores; copiar o Xip não entrega esses dois adapters.
As APIs oficiais têm suporte empresarial: o OpenAPI Ozav consultado declara
`CustomerCreateRequest.business` e `CustomerBusiness`; a BlindPay documenta
cadastro PJ e documentos da empresa e dos UBOs. A implementação precisa mapear
esses contratos, validar os requisitos e exercitar a jornada empresarial de
cada um antes de anunciar a capacidade.

Referências oficiais consultadas:

- [OpenAPI Ozav](https://developers.ozav.io/openapi.json).
- [Onboarding de clientes BlindPay](https://blindpay.com/prompts/integrate-customer-onboarding).
- [Documentos KYB BlindPay](https://blindpay.com/docs/kb/kyb-documents).
- [KYB Bridge](https://apidocs.bridge.xyz/platform/customers/compliance/businesses/overview).

Os filtros atuais de residência, identidade fiscal, token, rede e rail também
são parte da paridade. Não se deve habilitar todas as redes do Macro apenas
porque o wallet-service consegue assiná-las. Uma nova rede exige suporte e
validação no adapter de rampa correspondente.

## Modelo de titularidade

### Conta, titular e ator

- `Account` é o tenant e o limite de isolamento já existente no Macro.
- `Customer` é o titular verificado que envia ou recebe dinheiro. Pode ser a
  própria empresa da conta ou um cliente final PF/PJ.
- `User` é quem entra no admin. Um API token representa a integração de uma
  conta. Nenhum dos dois é automaticamente o titular de uma operação.
- `AssociatedPerson` é um sócio, controlador, diretor ou representante de
  uma empresa verificada; seu vínculo não é uma membership do admin.

Reutilizar a semântica de `Customer` do Xip, adaptando sua persistência ao
Macro. Não transportar a regra `individual customer_id == user_id`, a faixa
numérica especial para empresas ou o e-mail do usuário autenticado como
e-mail obrigatório do cliente final.

### Persistência proposta

As tabelas novas usam UUIDs e `account_id`, como o Macro. Uma FK composta ou
uma verificação transacional equivalente preserva a propriedade da conta em
cada vínculo. A simples presença de `customer_id` no request não autoriza
acesso.

| Tabela ou agregado | Responsabilidade |
|---|---|
| `customers` | `id`, `account_id`, `relationship` (`account_holder` ou `end_customer`), `entity_type` (`individual` ou `business`), `external_id`, nome, e-mail e lifecycle |
| `customer_individual_profiles` | Perfil PF cifrado e identificadores de busca protegidos |
| `customer_business_profiles` | Empresa, constituição, endereço, atividade e perfil operacional cifrados |
| `customer_associated_persons` | Pessoas relacionadas, papéis, participação e identificadores estáveis locais |
| `customer_provider_integrations` | Titular no provedor, contexto de credencial e ambiente, estado do vínculo |
| `customer_provider_kycs` | Aprovação, collection mode, termos, endorsements, pendências, rejeições e versão da submissão por provedor |
| `provider_kyc_submission_intents` | Intenção durável, upload checkpoints e recuperação de criação atômica |
| `customer_wallets` | Vínculo explícito entre titular e wallet/endereço Macro ou externo |
| `customer_bank_accounts`, `customer_pix_keys`, `customer_bank_beneficiaries` | Destinos e sua titularidade, revisão e estado |
| `customer_bank_beneficiary_provider_accounts` | Registro de cada beneficiário/destino no provedor |
| `customer_virtual_accounts`, `customer_liquidation_addresses` | Instruções de depósito e saque permanentes |
| `ramp_estimates` | Cotação, valores decimais, provedor escolhido, taxa, limites e expiração |
| `ramp_orders` | Lifecycle fiat/crypto e identidade de cada perna; FK para funding Macro quando aplicável |
| `offramp_funding_authorizations` | Ação preparada, revision, validade, assinatura autorizada e estado de finalização |
| `ramp_webhook_events` | Inbox autenticada, deduplicação, processamento e reprocessamento |
| `provider_calls` | Timeline operacional redigida, sem documentos nem segredos |
| `ramp_outbox_events` | Eventos duráveis para integrações dos tenants, publicados após commit |

`ramp_orders` fica separado das transações on-chain existentes; relacionar as
duas evita que uma cotação ou etapa fiat apareça como saldo confirmado de uma
wallet. Reaproveitar o ledger e os withdrawals do Macro para a perna on-chain.

Restrições mínimas:

- Um `account_holder` por conta/ambiente; seu cadastro é PJ neste escopo.
- `external_id` único dentro de conta/ambiente para clientes finais.
- Integração única por conta, titular, provedor e contexto de credencial/ambiente.
- Um identificador externo do provedor não pode ser relinkado a outro titular
  no mesmo contexto sem o procedimento explícito de identidade e auditoria.
- CPF/CNPJ e telefone não recebem unicidade global entre tenants por herança
  do Xip. Tratar a repetição dentro da conta e o conflito que o próprio
  provedor eventualmente impõe no contexto de suas credenciais.
- Destino, wallet, quote, order e funding pertencem à mesma conta e ao titular
  esperado. O `external_id` do cassino nunca substitui a FK interna validada.
- Teste e produção não compartilham customer IDs externos, saldos, quotes,
  instruções de depósito, assinaturas ou idempotency keys.

## API externa e admin

As rotas abaixo são o contrato proposto. Os dois controllers chamam os mesmos
services e resources; não há duas implementações do lifecycle.

- API de integrador: `/api/v1/*`, usando o API token da conta já existente.
- Admin: `/v1/*`, usando sessão, `X-Account-Id` e permissões da conta existentes.
- Operação global: `/v1/platform/*`, restrita aos operadores de plataforma.
- Webhook de provedor: rota própria que autentica o corpo com a credencial do
  provedor/ambiente antes de resolver o tenant a partir de vínculos persistidos.

### Recursos do integrador

| Métodos e caminho relativo | Caso de uso |
|---|---|
| `GET/PUT /account-holder` | Consultar ou preparar o cadastro da própria empresa |
| `GET/POST /customers` | Listar e cadastrar clientes finais, incluindo `external_id` |
| `GET/PATCH /customers/{id}` | Consultar e atualizar o cadastro permitido |
| `GET /customers/{id}/compliance` | Overview PF/PJ por provedor, pendências e rails abertos |
| `GET /customers/{id}/compliance/capabilities` | Capacidade efetiva por país, titular, provedor, rail, token e rede |
| `GET /customers/{id}/compliance/countries` | Países e identidades fiscais aceitos para esse fluxo |
| `GET/PUT /customers/{id}/compliance/profile` | Perfil retido; editar não significa ressubmeter ao provedor |
| `POST /customers/{id}/compliance/tos-sessions` | Abrir os termos dos provedores elegíveis |
| `POST /customers/{id}/compliance/tos-acceptances` | Registrar a evidência do aceite real do titular/representante |
| `POST /customers/{id}/compliance/applications` | Submeter PF ou PJ, com fan-out apenas aos provedores elegíveis |
| `POST /customers/{id}/compliance/documents` | Enviar documentos PF, da empresa e das pessoas relacionadas |
| `POST /customers/{id}/compliance/refresh` | Reconciliar a visão dos provedores |
| `GET /customers/{id}/compliance/providers/{provider}/requirements` | Pendências normalizadas de um provedor |
| `POST /customers/{id}/compliance/providers/{provider}/hosted-link` | Fluxo hospedado quando suportado |
| `POST /customers/{id}/compliance/providers/{provider}/endorsements` | Solicitar capacidades adicionais permitidas |
| `GET/POST/DELETE /customers/{id}/wallets[/{wallet}]` | Registrar e consultar wallets; remoção segue a policy apropriada |
| `GET/POST/PATCH/DELETE /customers/{id}/bank-accounts[/{bank}]` | Destinos bancários próprios |
| `GET/POST/DELETE /customers/{id}/pix-keys[/{key}]` | Chaves Pix e sua titularidade |
| `GET/POST/PATCH/DELETE /customers/{id}/beneficiaries[/{beneficiary}]` | Beneficiários quando suportados para o produto do tenant |
| `POST /customers/{id}/onramps/estimates` | Cotar fiat para crypto |
| `GET/POST /customers/{id}/onramps` | Criar e acompanhar onramps |
| `GET/POST/PATCH /customers/{id}/virtual-accounts[/{va}]` | Provisionar e manter instruções permanentes |
| `POST /customers/{id}/offramps/estimates` | Cotar crypto para fiat |
| `GET/POST /customers/{id}/offramps` | Criar e acompanhar offramps |
| `GET /customers/{id}/offramps/{order}` | Estado, pendências de funding e comprovante disponível |
| `POST /customers/{id}/offramps/{order}/funding-action/refresh` | Renovar ação preparada ainda válida para a cotação |
| `POST /customers/{id}/offramps/{order}/finalize` | Finalizar a ação assinada autorizada para essa ordem |
| `POST /customers/{id}/offramps/{order}/cancel` | Cancelar somente estados canceláveis |
| `GET/POST /customers/{id}/payout-addresses` | Liquidation addresses quando o provedor suportar |

O `account-holder` devolve seu `customer_id`; as operações dele usam as mesmas
rotas por titular. A API não recebe `account_id` arbitrário para escolher outra
empresa. O tenant autenticado e a consulta ao titular resolvem o escopo.

`Idempotency-Key` é exigida nos creates e finalizações que podem criar recursos
ou movimentar dinheiro. A combinação de conta, ambiente, titular, operação e
hash do body distingue replays; chave repetida com outro body responde conflito.
Requests assíncronos respondem `202` com o recurso/intenção que o integrador pode
consultar. Erros seguem o envelope e os machine codes existentes do Macro.

### Jornada do cassino

1. Backend do cassino cria/busca seu cliente usando `external_id` próprio.
2. Interface consulta capabilities e requirements e coleta os dados solicitados.
3. O cliente ou representante aceita os termos e envia os documentos.
4. Backend do cassino envia à API do Macro com seu token; o token não vai ao browser.
5. Macro submete, reconcilia e publica o estado normalizado por webhook do tenant.
6. Com o rail efetivamente liberado, o cassino solicita a cotação e cria o offramp.
7. Funding segue a wallet e a autorização válidas no Macro; a finalização fica
   vinculada à mesma quote, titular, token, valor, rede e destino.
8. O cassino acompanha eventos e consulta a ordem até a conclusão ou a resolução
   da pendência. Duplicar um request não duplica o pagamento.

O admin usa o mesmo fluxo em nome do titular, registrando o operador. O admin
não aceita termos no lugar de uma pessoa por simples clique em uma flag de KYC.
Links de aceite podem ser entregues ao titular pela integração do tenant.

Na integração server-to-server, o IP do backend do cassino ou do gateway Macro
não é o IP do cliente que aceitou os termos. Distinguir no contrato o contexto
de transporte do contexto do titular informado pelo integrador, com origem e
evidência registradas. Usar a confirmação do próprio provedor quando disponível;
um redirect ou um `signed_agreement_id` enviado pelo browser não prova o aceite.
URLs de retorno são validadas contra as origens cadastradas do tenant, e a
correlação do callback usa uma sessão vinculada à conta, titular, ambiente e
provedor. Não confiar em `X-Forwarded-For` arbitrário para atestar o titular.

### Eventos entregues ao tenant

Prever `customer.compliance.updated`, `customer.requirements.updated`,
`customer.virtual_account.updated`, `ramp.order.updated`, `ramp.order.completed`,
`ramp.order.failed` e `ramp.order.attention_required`. Os nomes finais entram no
catálogo de eventos do Macro com versionamento e payload documentado.

Cada evento contém identificador estável, conta, ambiente, titular, ordem quando
existir, `external_id` do tenant, versão e timestamp. A publicação usa outbox e o
delivery do Macro, com assinatura, retries e deduplicação pelo consumidor. Não
repassar o payload cru do provedor nem fotos, termos assinados ou dados bancários
completos. O recurso consultável é a fonte de verdade quando eventos chegam fora
de ordem.

## Settings e feature flags

Reutilizar o registry, a cifra, as policies e as telas de settings/flags já
presentes no Macro. Não instalar um segundo sistema de settings do Xip.

### Settings dos provedores

Copiar o inventário de campos e validadores de `app/settings/ramp_groups.go` e
o reader `app/adapters/ramp/settings.go`, adaptando-os ao registry nativo:

- Comuns: `enabled`, `mode`, `api_key` e contexto de ambiente.
- Bridge: `webhook_public_key` com validação de PEM/RSA.
- Ozav: `webhook_secret`, prefixo e coerência da API key.
- BlindPay: `instance_id`, `webhook_secret`, `solana_usdc_mint`,
  `solana_usdc_decimals`, `solana_usdt_mint`, `solana_usdt_decimals`,
  `solana_delegate_address`, `solana_rpc_url` e
  `production_solana_payout_confirmation` com permissão específica.

Segredos permanecem cifrados e omitidos das respostas e dos logs; blank mantém o
segredo existente. As factories leem a configuração em uso, permitindo rotação
sem restart. Não expor `base_url` e `auth_scheme` arbitrários nos forms.

O Macro tem contas `test` e `prod` no mesmo runtime. Credenciais e roteamento são
resolvidos pelo ambiente da conta autenticada. **Não copiar** a regra do Xip que
compara o modo somente com `APP_ENV`: ela não representa esse modelo. Manter
configurações de sandbox e produção separadas, incluindo assinaturas de webhook.

### Settings dos rails

Preservar todos os rails existentes na origem: `pix_payin`, `pix_payout`,
`usd_payin`, `usd_ach_payout`, `usd_wire_payout`, `ach_payout` e `wire_payout`.
Capacidade anunciada depende do adapter que realmente atende cada combinação.

- `provider_order`, validado contra os provedores capazes do rail.
- Fee percentual por provedor/rail, incluindo arredondamento e spread.
- `min_amount_brl`, `max_amount_brl`, `min_amount_brl_by_token`.
- `min_amount_usd`, `max_amount_usd` onde o rail os implementa.
- `ozav_offramp_funding_window_minutes`.
- `ozav_deposit_match_tolerance`.
- `ozav_delivery_max_attempts`.

Separar a configuração que apenas a plataforma escreve da política da conta
(limites e habilitações). Overrides de conta não podem ampliar uma restrição da
plataforma. Taxas de endereços permanentes já provisionados não são reescritas
retroativamente por um novo default.

### Flags

Preservar a intenção das flags de origem:

| Flag de origem | Default na origem | Adaptação de escopo |
|---|---|---|
| `ramp-payin-enabled` | Ligada | Global, conta e eventual suspensão explícita do titular |
| `ramp-payout-enabled` | Ligada | Global, conta e titular |
| `ramp-usd-payin-enabled` | Ligada | Global, conta e titular |
| `ramp-usd-payout-enabled` | Ligada | Global, conta e titular |
| `ramp-bank-payout-enabled` | Desligada | Global, conta e titular; preservar abertura explícita |
| `kyb` | Desligada, por usuário | No Macro aplica à criação PJ da conta/cliente, separada do papel do operador |
| `kyc-usa-residence-enabled` | Desligada, global | Preservar a distinção entre residência PF e constituição PJ |

Adicionar gates explícitos de produto para operações próprias do tenant e para
operações de seus clientes. Desligar o acesso do segundo caso não deve impedir a
empresa de concluir suas próprias operações já criadas, nem o contrário.

O gate efetivo combina produto habilitado para o tenant, global, conta, titular,
ambiente, configuração válida, capacidade do adapter e aprovação daquele titular
naquele rail. Uma flag ligada sozinha nunca significa KYC/KYB aprovado.

Flags interrompem novas operações. Reads, conciliação, reembolso e liquidação de
movimentos já financiados preservam os caminhos de conclusão da origem.

## Funding e assinatura no Macro

As wallets do Macro usam coassinatura MPC: `share_A` e `share_B`, com autorização
de movimentação e controles já existentes. A migração não deve criar uma chave
privada paralela nem supor que o worker consegue assinar sozinho.

Separar no contrato `customer_id`, `funding_wallet_id`, `beneficiary_id` e
`actor`. A wallet é validada no tenant e no contexto de titularidade antes da
cotação/funding. Uma wallet da tesouraria financiando uma operação de cliente
precisa de autorização explícita de operação em nome de terceiro e de suporte
do produto/provedor; não é inferida pelo acesso ao API token do cassino.

Reaproveitar os ports de withdrawal/coassinatura do Macro para transferências e
criar um port específico para assinar ações preparadas, quando necessário.
Preservar as validações do Xip para BlindPay: mint, decimals, delegate, owner,
fee payer, token program, ATA, amount, blockhash, action revision, expiry e o hash
dos bytes a assinar. Uma ação genérica recebida do browser não vira uma ordem
de saque autorizada.

Uma quote escolhe o provedor. Create, funding, finalize, webhook e reconciliação
ficam vinculados a ele. Um timeout após envio é estado desconhecido a recuperar,
não autorização para repetir o pagamento em outro provedor.

## Processamento assíncrono e operação

Portar os comportamentos, não apenas os handlers:

- Staging cifrado e temporário para documentos, com prazo de expurgo, retries
  limitados e sem fotos em logs, timeline ou payloads de eventos do tenant.
- Intenção de criação antes de chamar um provedor; checkpoint de uploads e
  recuperação de resultado desconhecido da criação atômica BlindPay.
- Lock distribuído e transições condicionais contra atualizações obsoletas.
- Inbox de webhook com assinatura do corpo bruto, janela antirreplay quando
  aplicável e chave de dedup por contexto do provedor/ambiente/evento.
- Eventos que identificam conta virtual, liquidation address, wallet gerenciada,
  quote ou payout antes de identificarem uma ordem local.
- Ozav onramp em duas fases: entrada fiat e entrega crypto são etapas distintas.
- Ozav offramp: crédito da wallet gerenciada dispara a liquidação correspondente;
  um depósito não pode financiar duas ordens.
- Bridge: lifecycle de contas virtuais, external accounts e liquidation drains.
- BlindPay: preparação, assinatura, envio, finalização, recovery, refund e atenção
  operacional, inclusive respostas desconhecidas e quotes já consumidas.
- Estado terminal e versionamento evitam regressões por replay/eventos antigos.
- Outbox publica eventos do tenant somente após persistência do novo estado.
- Timeline redigida, health dos provedores, pendências operacionais, reprocessamento
  e trilha de auditoria com operador/API token e titular separados.

Comandos de origem a mapear ao runtime SQS/Lambda/local existente do Macro:

`kyc:reconcile`, `kyc:recover-intents`, `kyc:resolve-intent`, `kyc:scrub-intents`,
`ramps:reconcile`, `ramps:sync-virtual-accounts`, `ramps:recover-finalizations`,
`ramps:prune-timeline` e `ramp:blindpay-check`.

Jobs de origem: `submit_kyc_job`, `process_ramp_webhook_job` e
`settle_offramp_job`. Acrescentar as filas, DLQ, permissões e agendamentos
necessários ao mecanismo atual do Macro. Copiar apenas os commands, sem ligá-los
a um executor real, não fecha a migração.

## Permissões e atividade do tenant

Separar grants de cadastro, PII, envio de documentos, submissão, movimentação,
alteração de credenciais, relink/unlink, reset de KYC, reprocessamento e resolução
de pendências. Preservar as distinções existentes na origem: `providers.*`,
`integrations.relink`, `integrations.unlink`, `kyc.*`, `customers.*`,
`ramps.attention.*` e confirmação de payout de produção.

Adaptar esses grants ao catálogo de policies, roles e API-token scopes do Macro.
Uma operação permitida ao usuário do admin não concede acesso equivalente a um
API token. Auditors continuam somente leitura; operações que movem fundos
mantêm os controles de TOTP/coassinatura existentes para sua audiência.

Para o caso de cassino, representar também a habilitação do tenant para a
atividade e para operar em nome de clientes. A documentação BlindPay distingue
gaming licenciado com disclosure/revisão adicional de atividades proibidas:
[atividades suportadas e proibidas](https://blindpay.com/docs/kb/prohibited-activities).
Essa condição de produto pertence à elegibilidade do tenant, separada do KYC
PF do jogador; o domínio deve expressar a decisão recebida do provedor.

## Admin do Macro

Reaproveitar HeroUI v3, o shell, cores, tipografia, auth via cookies, i18n em
en/pt-BR/es, formulário RHF/Zod e transporte `/v1` existentes. O admin Xip serve
de referência funcional; seus componentes HeroUI v2 não são substitutos diretos.

Telas necessárias:

1. KYB da própria empresa, em configuração da conta, com pessoas relacionadas,
   termos, documentos, pendências e capacidade por provedor.
2. Diretório de clientes finais PF/PJ, busca por `external_id`, filtros e cadastro.
3. Detalhe do cliente com perfil, compliance, representantes/UBOs, wallets,
   destinos, contas virtuais, ordens e timeline.
4. Fluxos de onramp/offramp que identificam claramente titular, wallet, destino,
   valor, cotação, fees, funding e estado, incluindo a empresa da conta.
5. Settings de provedores/rails por ambiente e gates de produto por tenant.
6. Flags, health, inbox de webhooks, reprocessamento e fila de atenção operacional.

As chaves de cache incluem conta, ambiente e titular. Trocar de conta não exibe
dados do cliente anterior. Dados de compliance não são persistidos em
localStorage ou em stores globais sem escopo. A jornada do cliente final é
documentada para a interface do cassino; não está no escopo criar um novo portal
público do Macro.

## Validação realizada e critério de conclusão

O levantamento comparou arquivos e branches dos dois produtos e confirmou os
estados dos PRs no GitHub. A execução local de
`go test ./app/adapters/ramp/...` no snapshot de referência passou nos pacotes
Bridge, Ozav e Solana transaction, mas o pacote raiz e BlindPay abortaram no boot
de testes por ausência de conexão ORM configurada (`HasTable` no boot de
`RampServiceProvider`). Isso não é validação end-to-end nem resultado verde da
suíte inteira. Nenhum KYC real foi submetido e nenhum fundo foi movimentado.

O plano de execução e a matriz de aceite ficam em
`../plans/2026-11-02-ramp-integrations.md`. O manifesto de fontes identifica os
arquivos de referência e dependências; não significa que foram portados.

Concluir exige comprovar ambos os tipos de titular, admin e API externa,
isolamento entre tenants e ambientes, os três provedores nas capacidades
anunciadas, KYB adicional que falta na origem, funding MPC, callbacks ao tenant,
reconciliação e recuperação. Settings ou telas sem esse ciclo não representam
uma integração completa.
