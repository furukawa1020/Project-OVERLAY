require 'sinatra'
require 'rqrcode'
require 'faye/websocket'
Faye::WebSocket.load_adapter('thin')
require_relative 'state_manager'
require_relative 'config'

set :bind, '0.0.0.0'
set :port, 4567

$manager = StateManager.new
$clients = []

get '/' do
  File.read(File.join('public', 'index.html'))
end

get '/admin' do
  File.read(File.join('public', 'admin.html'))
end

get '/connect' do
  # Get Local IP
  ip = Socket.ip_address_list.detect(&:ipv4_private?)&.ip_address || "localhost"
  url = "http://#{ip}:#{settings.port}"
  
  qrcode = RQRCode::QRCode.new(url)
  qr_svg = qrcode.as_svg(
    color: "000",
    shape_rendering: "crispEdges",
    module_size: 11,
    standalone: true
  )
  
  erb :connect, views: 'public', locals: { qr_svg: qr_svg, url: url }
end

get '/config' do
  content_type :json
  {
    danger_words: Config::DANGER_WORDS,
    thresholds: {
      no: Config::THRESHOLD_NO,
      split: Config::THRESHOLD_SPLIT
    }
  }.to_json
end

get '/cable' do
  if Faye::WebSocket.websocket?(env)
    ws = Faye::WebSocket.new(env)

    ws.on :open do |event|
      $clients << ws
      # Send current state
      ws.send($manager.get_state.to_json)
    end

    ws.on :message do |event|
      data = JSON.parse(event.data) rescue {}
      
      case data['type']
      when 'speech_text'
        # Goからの音声認識テキストを受信
        $manager.process_text(data['text'])
        broadcast_state
      when 'vote'
        # Legacy/Mobile input (Keep only for manual override if needed)
        # $manager.add_vote(data['vote']) 
        broadcast_state
      when 'flash_word'
        # 参加者が危険ワードを押した場合
        # 現状は即座にFlashさせる（要件によっては投票制にするが、露出重視なら即時で良い）
        broadcast_flash(data['word'])
      when 'admin'
        handle_admin_command(data)
      end
    end

    ws.on :close do |event|
      $clients.delete(ws)
      ws = nil
    end

    ws.rack_response
  else
    "This is a WebSocket endpoint."
  end
end

def handle_admin_command(data)
  case data['command']
  when 'reset'
    $manager.reset
    broadcast_state
  when 'flash'
    broadcast_flash(data['word'])
  end
end

def broadcast_flash(word)
  msg = { type: 'flash', word: word, ttl: 1000 }.to_json
  $clients.each { |ws| ws.send(msg) }
end

def broadcast_state
  state = $manager.get_state.to_json
  $clients.each { |ws| ws.send(state) }
end

# Background thread to update state (decay) every second
Thread.new do
  loop do
    sleep 1
    broadcast_state
  end
end
