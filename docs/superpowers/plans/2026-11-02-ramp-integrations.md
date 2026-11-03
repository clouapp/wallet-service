# Plano de implementação das integrações de rampas

Data: 2026-11-02. Este plano parte do
[desenho de tenants e clientes](../specs/2026-11-02-ramp-integrations-design.md).
O port local foi implementado na branch abaixo. Este plano é o registro original;
consulte o [runbook implementado](../../RAMP_INTEGRATIONS.md) para contratos finais,
adaptações, testes e limitações. Deploy e validação externa em testnet permanecem
fora da execução local.

## Fontes e bases

Backend alvo: worktree `wallet-service-ramps`, branch
`feat/admin-ramp-integrations`, a partir de `origin/main` `42871a36`.
Frontend alvo: worktree `macro-wallets-front-ramps`, branch de mesmo nome,
a partir de `origin/main` `9fe65ef`.

Os checkouts principais não devem ser resetados. Não há deploy, migração de
produção, importação de dados reais ou publicação de PR incluídos no levantamento.

## Matriz de rastreabilidade

| Área | Referências no backend Xip | Destino no Macro |
|---|---|---|
| Contrato e adapters | `app/adapters/ramp/`, inclusive `blindpay/`, `bridge/`, `ozav/`, `solanatx/` | `app/adapters/ramp/`, ports de stores estreitos e tipos nativos |
| Titulares PF/PJ | `app/models/customer*`, `app/services/client/customers/`, `app/services/admin/customers/` | Domínio de customers com UUID, conta e dois relacionamentos |
| KYB e UBO | `app/services/client/kyc/business_*`, `app/adapters/ramp/bridge/business_*` | Mesmo lifecycle para PJ da conta e PJ cliente; completar Ozav/BlindPay |
| Compliance | `app/services/client/kyc/`, `app/dto/api/client/ramps/` | Services/DTOs independentes da audiência |
| Vínculos externos | `app/services/client/integration/`, repositories correspondentes | Identidade por conta, titular, provedor e ambiente |
| Destinos | `app/services/client/{bank,pixkeys}/`, `ramps/{bank_account_service,pix_account}*` | Beneficiários, titularidade e vínculos com provedores |
| Ordens | `app/services/client/ramps/` e `app/services/ramp/` | Quote, reserva, funding, lifecycle e projeção nativos |
| Assinaturas preparadas | `app/adapters/ramp/{offramp_funding,offramp_finalize}*`, `blindpay/{prepare,broadcast,finalize}*` | Coassinatura Macro e validações preservadas |
| Webhooks | `app/services/ramp/webhook*`, `app/http/controllers/webhooks/`, `routes/webhooks.go` | Inbox, verificação, dedup, tenant resolution e SQS |
| Settings | `app/settings/ramp_groups.go`, `app/adapters/ramp/settings.go` | Registry nativo `app/services/settings/`, seções existentes |
| Flags | `app/featureflags/{ramp,kyc,customers}/` | Catálogo nativo com global, conta e titular |
| Operação | `app/services/admin/{providers,kyc,ramps}/`, `app/console/commands/*{ramp,kyc}*` | Services e controllers de plataforma, workers e scheduler |
| Wiring | `app/providers/{ramp,kyc,customer}_service_provider.go`, `bootstrap/{jobs,migrations}.go` | Providers, typed container e bootstrap existentes |
| Auditoria e eventos | `provider_calls`, request redaction, broadcast e notificações | Activity log nativo, outbox e webhooks do tenant |
| Admin | `xipcash/admin/app/(dashboard)/customers/`, `lib/{customers,providers,kyc-profile,bank-beneficiaries}/` | Views Macro, HeroUI v3, RHF/Zod, auth e cache nativos |

O manifesto JSON em `docs/ramp-integrations-source-manifest.json` enumera
arquivos, hashes e classificações. Dependências transversais são referências,
não uma instrução para copiar auth, CMS ou todos os serviços do Xip.

## Sequência de entrega

### 1. Titularidade e isolamento

- [ ] Criar models, migrations, resources, repositories e services de customers.
- [ ] Implementar `account_holder` PJ e `end_customer` PF/PJ.
- [ ] Separar operador/API token, representante e cliente verificado.
- [ ] Implementar lookup por `external_id` dentro da conta.
- [ ] Aplicar escopo da conta em todas as consultas e vínculos.
- [ ] Definir leitura/escrita e scopes da API no catálogo de policies existente.
- [ ] Cobrir referências cruzadas entre contas e ambientes com testes negativos.

### 2. Contratos e configuração dos três provedores

- [ ] Portar contracts, money/fee helpers, status maps e adapters com seus testes.
- [ ] Substituir dependências amplas de repositories por ports consumidores.
- [ ] Adaptar UUIDs e contexto de ambiente sem alterar identificadores externos.
- [ ] Registrar settings, validações, secrets, rotação e factories.
- [ ] Configurar rails, provider order, fees e limites.
- [ ] Implementar flags globais, de conta, de titular e dos dois casos de uso.
- [ ] Não habilitar provider/rail/rede que não tenha implementação funcional.

### 3. KYC e KYB compartilhados entre admin e API

- [ ] Implementar perfis PF/PJ e pessoas relacionadas, com cifra e acesso separado.
- [ ] Portar termos, aceite, hosted fallback, endorsements e requirements.
- [ ] Correlacionar callbacks de termos e capturar a proveniência do contexto
  do titular, sem confundir o IP do cassino/gateway com o IP do cliente final.
- [ ] Portar validações de país, CPF/CNPJ/passaporte/SSN/ITIN/EIN e vocabulários.
- [ ] Portar fan-out elegível, idempotência, atualização condicional e reset.
- [ ] Portar staging, filas, intenção atômica BlindPay e upload checkpoints.
- [ ] Completar o adapter empresarial Ozav pelo contrato oficial.
- [ ] Completar o adapter empresarial BlindPay, inclusive documentos dos UBOs.
- [ ] Separar habilitação do tenant para o produto/atividade da aprovação de cada titular.
- [ ] Expor o mesmo service nas duas audiências, mantendo seus mecanismos de auth.

### 4. Destinos e onramp

- [ ] Implementar chaves Pix, contas bancárias, beneficiários e seus registros externos.
- [ ] Vincular wallets ao titular e à conta autenticada.
- [ ] Portar estimativa, limites, fees, reserva, create e lifecycle de onramp.
- [ ] Portar virtual accounts BRL/USD e atualização de destino.
- [ ] Preservar Ozav em duas fases, delivery idempotente e limite de tentativas.
- [ ] Refletir somente a liquidação confirmada no ledger Macro.

### 5. Offramp e funding MPC

- [ ] Integrar quotes/ordens ao funding e à autorização de wallet existentes.
- [ ] Portar Bridge transfers e liquidation addresses/drains.
- [ ] Portar Ozav managed-wallet deposit e settle com match único de depósito.
- [ ] Portar BlindPay Pix/ACH/wire, prepare, action refresh, finalize e recovery.
- [ ] Preservar validações da transação Solana e vínculo quote/ordem/ação/destino.
- [ ] Aplicar permissões, limites e segundo fator pertinentes a cada audiência.
- [ ] Tratar cancellation, quote expirado, refund, returning e atenção operacional.
- [ ] Não repetir nem trocar de provedor depois de resultado de envio desconhecido.

### 6. Eventos e recuperação operacional

- [ ] Implementar inbox autenticada por provedor/credencial/ambiente.
- [ ] Mapear eventos de customer, termos, conta, transferência, depósito e drain.
- [ ] Deduplicar e impedir regressão de eventos antigos.
- [ ] Implementar outbox e callbacks assinados para os tenants.
- [ ] Adaptar jobs, SQS, DLQ, retries e schedules ao runtime Macro.
- [ ] Portar os nove commands listados no desenho, com execução efetiva.
- [ ] Disponibilizar health, timeline redigida, reprocessamento e fila de atenção.

### 7. Admin e experiência de integração

- [ ] KYB da própria empresa na configuração da conta.
- [ ] Clientes PF/PJ, busca por ID do integrador e detalhe de compliance.
- [ ] Wizard com dados, pessoas relacionadas, termos e documentos.
- [ ] Destinos, wallets, contas virtuais e operações dos dois tipos de titular.
- [ ] Settings de provedores/rails por ambiente e flags com permissões corretas.
- [ ] Telas de status, requirements, recovery e histórico.
- [ ] API reference e exemplos para a interface e o backend do cassino.
- [ ] Traduções en/pt-BR/es e cache com conta/ambiente/titular.

## Matriz de aceite

Executar em sandbox e com fakes determinísticos; não usar dinheiro real para
validar idempotência, falhas ou permissões.

| Caso | Evidência exigida |
|---|---|
| Tenant PJ próprio | KYB, documentos da empresa e UBOs, aprovação por provedor e operação própria pelo admin/API |
| Cliente PF do cassino | Cadastro por `external_id`, KYC, termos/documentos, callback, cotação, funding e offramp pela API |
| Cliente PJ | Mesmo caminho com KYB e pessoas relacionadas, independente da empresa tenant |
| Três provedores | Fluxos funcionais para cada capacidade anunciada; empresa jamais recebe capability inventada |
| Autorização | Auditor não move fundos; API token sem scope não submete ou paga; suspensão é aplicada no backend |
| Isolamento | Conta A não consulta, altera, assina, financia ou reprocessa dados da B; teste não toca produção |
| Estado de KYC | Aprovação de um titular/provedor/rail não libera outro; pendências e rejeições chegam à interface correta |
| Fan-out | Um provedor indisponível não apaga o resultado dos demais; retry não duplica customer nem upload |
| Idempotência | Mesmo key/body retorna a mesma intenção/ordem; key com outro body dá conflito |
| Funding | Outra wallet, token, mint, delegate, amount, revision ou assinatura é recusada antes de executar |
| Quote | Expirada/consumida, pertencente a outro titular/rail ou provider não é resgatada |
| Webhooks | Assinatura falsa recusada; eventos duplicados, atrasados e de outro ambiente não alteram o recurso indevido |
| Recuperação | Timeout após envio, processo morto após upload, callback perdido e finalize desconhecido têm caminho de conciliação |
| Desativação | Flag desliga novas operações, preservando o encerramento das já financiadas |
| Settings | Secret ausente/blank/rotacionado, mode mismatch, PEM inválido e confirmação de produção são testados |
| Callback ao cassino | Evento assinado, retry de entrega, replay e consulta do estado atual documentados |
| PII | Documentos e segredos ausentes de logs, timeline pública, respostas indevidas e eventos do tenant |
| UI | Fluxos em desktop/mobile, três idiomas, erros por campo, troca de conta e gravação smoke com backend real local |

Rodar as suítes e verificações de arquitetura do Macro por grupo. Não considerar
o plano concluído por compilar adapters ou exibir mocks no admin. Marcar cada
item somente com implementação ligada ao runtime e evidência correspondente.
