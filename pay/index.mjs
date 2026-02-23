#!/usr/bin/env node
import readline from 'readline/promises'
import { stdin as input, stdout as output } from 'process'
import { WalletClient, IdentityClient, PublicKey, Utils } from '@bsv/sdk'
import { PeerPayClient } from '@bsv/message-box-client'

// ---------------------------------------------------------------------------
// Config
// ---------------------------------------------------------------------------

const MESSAGE_BOX_URL = process.env.MESSAGE_BOX_URL || 'https://messagebox.babbage.systems'

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

const wallet = new WalletClient('auto', 'pay')
const identityClient = new IdentityClient(wallet)

const peerPay = new PeerPayClient({
  walletClient: wallet,
  messageBoxHost: MESSAGE_BOX_URL,
  enableLogging: false,
})

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function isHexPublicKey(str) {
  return /^0[23][0-9a-fA-F]{64}$/.test(str)
}

async function resolveRecipient(target) {
  if (isHexPublicKey(target)) {
    return target
  }
  process.stdout.write(`Resolving ${target} ...\n`)
  const results = await identityClient.resolveByAttributes({ any: target }, 5)
  if (!results || results.length === 0) {
    throw new Error(`No identity found for "${target}"`)
  }
  const key = results[0].identityKey
  process.stdout.write(`Found identity key: ${key}\n`)
  return key
}

async function cmdPay(args) {
  if (args.length < 2) {
    console.log('Usage: /pay <recipient> <satoshis>')
    console.log('  recipient — 66-char hex identity key, or a name/email to look up')
    console.log('  satoshis  — integer amount in satoshis')
    return
  }
  const [target, amountStr] = args
  const amount = parseInt(amountStr, 10)
  if (isNaN(amount) || amount <= 0) {
    console.log('Error: satoshis must be a positive integer.')
    return
  }

  let recipient
  try {
    recipient = await resolveRecipient(target)
  } catch (err) {
    console.log(`Error resolving recipient: ${err.message}`)
    return
  }

  process.stdout.write(`Sending ${amount.toLocaleString()} sats to ${recipient.slice(0, 16)}... \n`)
  try {
    await peerPay.sendPayment({ recipient, amount })
    console.log('Payment sent successfully!')
  } catch (err) {
    console.log(`Error sending payment: ${err.message}`)
  }
}

async function cmdReceive() {
  process.stdout.write('Checking for inbound payments ...\n')
  let payments
  try {
    payments = await peerPay.listIncomingPayments(MESSAGE_BOX_URL)
  } catch (err) {
    console.log(`Error listing payments: ${err.message}`)
    return
  }

  if (!payments || payments.length === 0) {
    console.log('No pending payments.')
    return
  }

  payments.forEach((p, i) => {
    const sats = p.token?.amount ?? '?'
    const sender = p.sender ? p.sender.slice(0, 14) + '...' : 'unknown'
    console.log(`  [${i + 1}] ${Number(sats).toLocaleString()} sats from ${sender}`)
  })

  let accepted = 0
  for (let i = 0; i < payments.length; i++) {
    const p = payments[i]
    process.stdout.write(`Accepting payment ${i + 1} ... `)
    try {
      await peerPay.acceptPayment(p)
      process.stdout.write('done.\n')
      accepted++
    } catch (err) {
      // Retry once with a fresh listing in case the token is stale
      try {
        const fresh = await peerPay.listIncomingPayments(MESSAGE_BOX_URL)
        const match = fresh.find(x => String(x.messageId) === String(p.messageId))
        if (!match) throw new Error('Payment not found on refresh')
        await peerPay.acceptPayment(match)
        process.stdout.write('done.\n')
        accepted++
      } catch (err2) {
        process.stdout.write(`failed: ${err2.message}\n`)
      }
    }
  }
  console.log(`${accepted} payment${accepted !== 1 ? 's' : ''} received.`)
}

async function cmdBalance() {
  try {
    const result = await wallet.getBalance({})
    const total = (result.total ?? result.satoshis ?? 0)
    console.log(`Balance: ${Number(total).toLocaleString()} sats`)
  } catch (err) {
    console.log(`Error fetching balance: ${err.message}`)
  }
}

async function cmdHistory() {
  try {
    const response = await wallet.listActions({
      labels: ['peerpay'],
      labelQueryMode: 'any',
      includeOutputs: true,
      includeOutputLockingScripts: true,
      limit: 20,
    })
    const actions = response?.actions ?? []
    if (actions.length === 0) {
      console.log('No payment history found.')
      return
    }
    console.log('Recent payments:')
    for (const action of actions) {
      const sats = action.satoshis ?? 0
      const dir = sats < 0 ? 'sent' : 'received'
      const abs = Math.abs(sats)
      console.log(`  ${dir.padEnd(8)} ${abs.toLocaleString().padStart(12)} sats  txid: ${action.txid?.slice(0, 16)}...`)
    }
  } catch (err) {
    console.log(`Error fetching history: ${err.message}`)
  }
}

function cmdHelp() {
  console.log('')
  console.log('Available commands:')
  console.log('  /pay <recipient> <satoshis>  Send a BRC-29 payment')
  console.log('  /receive                     List and accept inbound payments')
  console.log('  /balance                     Show wallet balance')
  console.log('  /history                     Show recent payment history')
  console.log('  /help                        Show this help')
  console.log('  /quit                        Exit')
  console.log('')
  console.log('recipient can be a 66-char hex identity key, or a name/email/paymail')
  console.log(`Message Box: ${MESSAGE_BOX_URL}`)
  console.log('')
}

// ---------------------------------------------------------------------------
// REPL
// ---------------------------------------------------------------------------

async function main() {
  console.log('Gebunden Pay CLI — BRC-29 payments')
  console.log(`Wallet: WalletClient('auto', 'pay')`)
  console.log(`Message Box: ${MESSAGE_BOX_URL}`)
  console.log("Type /help for available commands.\n")

  const rl = readline.createInterface({ input, output, terminal: true })

  rl.on('close', () => {
    console.log('\nGoodbye.')
    process.exit(0)
  })

  while (true) {
    let line
    try {
      line = await rl.question('> ')
    } catch {
      break
    }

    const trimmed = line.trim()
    if (!trimmed) continue

    const [cmd, ...args] = trimmed.split(/\s+/)

    switch (cmd.toLowerCase()) {
      case '/pay':
        await cmdPay(args)
        break
      case '/receive':
        await cmdReceive()
        break
      case '/balance':
        await cmdBalance()
        break
      case '/history':
        await cmdHistory()
        break
      case '/help':
        cmdHelp()
        break
      case '/quit':
      case '/exit':
        rl.close()
        process.exit(0)
        break
      default:
        console.log(`Unknown command: ${cmd}. Type /help for available commands.`)
    }
  }
}

main().catch(err => {
  console.error('Fatal error:', err.message)
  process.exit(1)
})
