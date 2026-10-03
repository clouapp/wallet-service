package keyexport

import (
	"fmt"
	"strings"
)

// RenderInstructions is INSTRUCOES.md: a security warning, what each file holds, and
// how to import, use and recover the keys of every network.
func RenderInstructions(manifest Manifest) string {
	var doc strings.Builder
	doc.WriteString(instructionsHeader)
	writeWalletTable(&doc, manifest)
	doc.WriteString(instructionsBody)
	return doc.String()
}

func writeWalletTable(doc *strings.Builder, manifest Manifest) {
	fmt.Fprintf(doc, "\nGerado em %s (APP_ENV=%s). Formato %d. Cifra do zip: %s.\n\n", manifest.GeneratedAt, manifest.AppEnv, manifest.FormatVersion, manifest.Encryption)
	doc.WriteString("## Wallets neste arquivo\n\n")
	doc.WriteString("| Label | Wallet ID | Chain | Rede | Tipo | Endereços | Pasta |\n|---|---|---|---|---|---|---|\n")
	for _, wallet := range manifest.Wallets {
		kind := networkKind(wallet.Testnet)
		if !wallet.Testnet {
			kind = "**MAINNET**"
		}
		if wallet.AllEVMChains {
			kind += " (chave EVM: vale em todas as redes EVM, inclusive mainnets)"
		}
		fmt.Fprintf(doc, "| %s | `%s` | %s | %s | %s | %d | `%s` |\n",
			markdownCell(wallet.Label), wallet.WalletID, wallet.Chain, wallet.Network, kind, wallet.AddressCount, wallet.Directory)
	}
	if len(manifest.Refused) > 0 {
		doc.WriteString("\n### Wallets RECUSADAS (não estão neste arquivo)\n\n")
		for _, refused := range manifest.Refused {
			fmt.Fprintf(doc, "- `%s` (%s, %s): %s\n", refused.WalletID, markdownCell(refused.Label), refused.Chain, markdownCell(refused.Reason))
		}
	}
	doc.WriteString("\n")
}

func markdownCell(value string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(value)
}

const instructionsHeader = `# Exportação de chaves privadas — Macro Wallets

> ## ⚠️ AVISO DE SEGURANÇA — LEIA ANTES DE QUALQUER COISA
>
> - Este arquivo contém **CHAVES PRIVADAS e as DUAS shares MPC** de cada wallet listada.
>   Quem tiver este zip **e** a senha controla **todos os fundos** destes endereços,
>   sem precisar da plataforma, da passphrase da wallet nem do AWS Secrets Manager.
> - A exportação **anula o modelo MPC** (nenhuma parte sozinha deveria ter a chave):
>   trate o conteúdo como dinheiro em espécie.
> - Abra e use **somente em máquina confiável, de preferência offline**. Nunca envie por
>   e-mail/chat, nunca faça commit em repositório, nunca cole chaves em sites.
> - Os **nomes** dos arquivos dentro do zip (ids de wallet, índices) ficam visíveis sem a
>   senha; só o **conteúdo** é cifrado.
> - Chaves **EVM** valem para o mesmo endereço em **todas** as redes EVM (Ethereum, Polygon,
>   BSC, Base, Arbitrum…), **inclusive mainnet**, mesmo que a chain esteja configurada para
>   testnet aqui.
> - Depois de usar, destrua as cópias (` + "`shred -u arquivo.zip`" + ` e dos arquivos extraídos) e,
>   se a exportação vazou, mova os fundos para novas wallets imediatamente.
`

const instructionsBody = `## Como abrir este zip

O zip usa **WinZip AES-256 (AE-2)**. Abre com 7-Zip (` + "`7z x arquivo.zip`" + `), WinZip,
PeaZip ou Keka (macOS). **Não** abre com o ` + "`unzip`" + ` do Info-ZIP nem com o
descompactador nativo do Windows/macOS (não suportam AES). Extraia para um diretório
privado (` + "`mkdir -m 700`" + `) em disco que você vai destruir depois.

## Conteúdo

- ` + "`manifest.json`" + ` — resumo (wallets, redes, contagem de endereços, wallets recusadas). Sem chaves.
- ` + "`wallets/<wallet-id>/wallet.json`" + ` — por wallet: chain, rede (nome e testnet/mainnet),
  chave pública da wallet, chain code e, para cada endereço do banco (base/genesis e filhos,
  ativos e retirados), tipo e índice de derivação, chave pública e a chave privada nos formatos
  importáveis da rede. Cada chave foi verificada: o endereço re-derivado **da própria chave
  exportada** (campo ` + "`verified_address`" + `) é idêntico ao endereço do banco. Wallets em que isso
  falhou foram recusadas e não estão aqui.
- ` + "`wallets/<wallet-id>/share_a.json`" + ` — share A (cliente), **decifrada** (JSON do tss-lib v2).
- ` + "`wallets/<wallet-id>/share_b.json`" + ` — share B (serviço, AWS Secrets Manager), JSON do tss-lib v2.
- ` + "`wallets/<wallet-id>/solana-keygen/index-N.json`" + ` — (Solana, endereços filhos) keypair no
  formato do solana-keygen.

## EVM (Ethereum, Polygon, BSC, Base, Arbitrum)

Campo ` + "`addresses[].evm.private_key_hex`" + ` (` + "`0x`" + ` + 64 hex). Endereço base (` + "`genesis`" + `)
= chave da wallet; filhos (` + "`bip32`" + `) = chave da wallet + tweak BIP-32 não-hardened do índice.

- **MetaMask**: menu de contas → *Adicionar conta ou carteira de hardware* → *Importar conta*
  → tipo *Chave privada* → cole o valor. Confira que o endereço mostrado é o ` + "`address`" + ` do JSON.
  Depois selecione/adicione a rede desejada (o endereço é o mesmo em todas).
- **Foundry** (conferência sem expor no histórico): ` + "`cast wallet address --interactive`" + ` e cole a chave.
- Tokens ERC-20 ficam no mesmo endereço; importe o contrato do token na carteira para vê-los.

## Bitcoin

Campos ` + "`addresses[].bitcoin`" + `: ` + "`wif_compressed`" + ` (WIF comprimida; prefixo ` + "`c`" + `
em testnet, ` + "`K`/`L`" + ` em mainnet), ` + "`electrum_import`" + ` (` + "`p2wpkh:<WIF>`" + `),
` + "`descriptor`" + ` (` + "`wpkh(<WIF>)`" + `) e ` + "`descriptor_with_checksum`" + `. Endereços são P2WPKH
(bech32, ` + "`tb1…`" + ` em testnet, ` + "`bc1…`" + ` em mainnet). O campo ` + "`network`" + ` diz para
qual rede a WIF foi gerada (testnet3/testnet4/signet compartilham o mesmo prefixo de WIF).

- **Electrum**: *Arquivo → Novo/Restaurar* → nome → *Importar endereços Bitcoin ou chaves
  privadas* → cole uma linha ` + "`p2wpkh:<WIF>`" + ` por endereço. O prefixo ` + "`p2wpkh:`" + ` é
  obrigatório (sem ele o Electrum gera o endereço legado errado). Para testnet inicie com
  ` + "`electrum --testnet`" + ` (para testnet4, use uma versão do Electrum que suporte essa rede ou o
  Bitcoin Core).
- **Sparrow**: *Tools → Sweep Private Key* → cole a WIF, *Script Type* = *Native Segwit (P2WPKH)*
  → escolha uma wallet de destino → *Create Transaction*. (Inicie o Sparrow na rede certa:
  testnet/mainnet.)
- **Bitcoin Core** (descriptor wallet), troque ` + "`-testnet`" + ` por ` + "`-testnet4`" + ` (Core ≥ 28) ou
  remova para mainnet:

` + "```" + `
bitcoin-cli -testnet -named createwallet wallet_name=recuperacao blank=true descriptors=true
bitcoin-cli -testnet -rpcwallet=recuperacao importdescriptors '[{"desc":"<descriptor_with_checksum>","timestamp":0}]'
bitcoin-cli -testnet -rpcwallet=recuperacao getbalances
` + "```" + `

  ` + "`timestamp: 0`" + ` faz rescan completo (lento); use o horário de criação da wallet para acelerar.
  O checksum após ` + "`#`" + ` já vem calculado (BIP-380); ` + "`getdescriptorinfo`" + ` confirma.
  Cuidado: o comando fica no histórico do shell — rode com ` + "`HISTCONTROL=ignorespace`" + ` e um espaço
  inicial, ou use o console do bitcoin-qt.

## Solana

### Endereços filhos (` + "`derivation_type: slip0010`" + `) — importáveis

Campos ` + "`addresses[].solana`" + `: ` + "`keypair_base58`" + ` (segredo de 64 bytes = seed ‖ chave
pública), ` + "`keypair_json_bytes`" + ` (mesmo segredo como array JSON) e ` + "`seed_hex`" + `
(seed RFC 8032 de 32 bytes). São chaves ed25519 comuns.

- **Phantom**: *Adicionar/Conectar carteira* → *Importar chave privada* → cole ` + "`keypair_base58`" + `.
- **solana CLI**: use o arquivo ` + "`solana-keygen/index-N.json`" + ` da pasta da wallet:
  ` + "`solana-keygen pubkey index-N.json`" + ` (deve imprimir o endereço) e
  ` + "`solana balance --keypair index-N.json --url devnet`" + ` (ou ` + "`mainnet-beta`" + `).
  Para transferir: ` + "`solana transfer --keypair index-N.json --url devnet <destino> <SOL>`" + `
  (acrescente ` + "`--allow-unfunded-recipient`" + ` se o destino ainda não existir).

### Endereço base / genesis (` + "`derivation_type: genesis`" + `) — NÃO importável em Phantom/solana-keygen

A chave do endereço base de uma wallet MPC ed25519 **não é uma seed**: o MPC (tss-lib) gera
diretamente o **escalar** ` + "`a`" + ` com ` + "`a·B = chave pública`" + `, reconstruído por
interpolação de Lagrange das duas shares. Phantom, solana-keygen e toda carteira RFC 8032 só
aceitam uma **seed** de 32 bytes e calculam o escalar como ` + "`clamp(SHA-512(seed)[0..32])`" + `.
Não existe seed que produza este escalar (seria inverter o SHA-512), então **não há formato
importável**. Exportamos o escalar em ` + "`addresses[].solana_scalar`" + `:
` + "`scalar_hex_big_endian`" + ` (como o backend reconstrói) e ` + "`scalar_hex_little_endian`" + `
(codificação RFC 8032 de escalares). ` + "`importable_in_phantom_or_solana_keygen`" + ` é ` + "`false`" + `.

Como recuperar fundos do endereço genesis:

1. **Pela plataforma (recomendado)**: enquanto a plataforma e as shares existirem, faça um
   saque/sweep a partir do endereço base. O backend assina com esse mesmo escalar
   (` + "`app/services/chain/ed25519_scalar.go`" + `, ` + "`signEd25519WithScalar`" + `).
2. **Offline, com uma biblioteca que assine com escalar** (sem seed): uma assinatura Ed25519
   válida é ` + "`R = r·B`" + `, ` + "`S = r + SHA-512(R ‖ A ‖ msg)·a mod L`" + `, com ` + "`r`" + `
   secreto e único por mensagem. Em Go, ` + "`filippo.io/edwards25519`" + ` faz isso (é o que o
   backend usa); outras bibliotecas expõem "expanded secret key"/"raw sign" — confira que elas
   **não aplicam clamping** ao escalar (o escalar daqui é reduzido mod L e não está clampado).
   Monte a transação Solana (ex.: com ` + "`@solana/web3.js`" + `), assine a mensagem serializada com
   o escalar, anexe a assinatura e envie. Valide primeiro que ` + "`escalar·B`" + ` codificado em
   base58 é exatamente o ` + "`address`" + `.
3. **A partir das shares**: ` + "`share_a.json`" + ` + ` + "`share_b.json`" + ` reconstroem o mesmo
   escalar (ver abaixo); útil se o ` + "`wallet.json`" + ` se perder.

## Shares MPC e reconstrução manual

As shares são ` + "`LocalPartySaveData`" + ` do tss-lib v2 em JSON (campos ` + "`Xi`" + ` = share
secreta, ` + "`ShareID`" + ` = abscissa, ` + "`ECDSAPub`/`EDDSAPub`" + ` = chave pública da wallet).

- **secp256k1**: chave da wallet = interpolação de Lagrange em 0 das duas shares
  (` + "`(Xi_A·id_B − Xi_B·id_A)/(id_B − id_A) mod n`" + `, ` + "`vss.Shares.ReConstruct`" + ` no
  tss-lib). Filho de índice i (` + "`bip32`" + `): BIP-32 não-hardened a partir da chave pública
  comprimida da wallet e do ` + "`chain_code_hex`" + `: ` + "`IL = HMAC-SHA512(chain_code, pubkey ‖ ser32(i))[0..32]`" + `,
  chave filha = chave da wallet + IL mod n.
- **ed25519 genesis**: escalar = interpolação de Lagrange das duas shares (mod L), verificado
  contra a chave pública.
- **ed25519 filhos** (` + "`slip0010`" + `): master = ` + "`(Xi_A + Xi_B) mod L`" + ` (soma simples das
  shares, 32 bytes big-endian — **não** é a chave do endereço base) e seed do filho =
  ` + "`HMAC-SHA512(chain_code, 0x00 ‖ master ‖ ser32(i + 2^31))[0..32]`" + ` (SLIP-0010
  hardened). Essa seed também fica gravada cifrada (Argon2id + AES-GCM com a passphrase da
  wallet) na linha do endereço; ` + "`key_source`" + ` diz qual das duas foi exportada.

## Em caso de dúvida

Não importe nada em carteira online "para testar" com fundos de mainnet. Prefira mover
fundos pela plataforma. Se este arquivo pode ter vazado, considere todas as chaves aqui
comprometidas e transfira os fundos para wallets novas.
`
