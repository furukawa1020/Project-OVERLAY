require 'sinatra'
require 'faye/websocket'
require 'json'

require_relative 'state_manager'

set :server, 'thin'
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
      when 'vote'
        $manager.add_vote(data['vote'])
        broadcast_state
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
    # Broadcast flash event directly to clients (Go renderer)
    msg = { type: 'flash', word: data['word'], ttl: 1000 }.to_json
    $clients.each { |ws| ws.send(msg) }
  end
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
