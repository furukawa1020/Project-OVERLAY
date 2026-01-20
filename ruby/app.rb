require 'sinatra'
require 'faye/websocket'
require 'json'

set :server, 'thin'
set :bind, '0.0.0.0'
set :port, 4567

# Global State (In-Memory for now)
$state = {
  current: 'UNKNOWN', # ALIGNED, SPLIT, UNKNOWN
  split_degree: 0.0,
  strength: 0.0,
  clients: []
}

get '/' do
  File.read(File.join('public', 'index.html'))
end

get '/cable' do
  if Faye::WebSocket.websocket?(env)
    ws = Faye::WebSocket.new(env)

    ws.on :open do |event|
      $state[:clients] << ws
      puts "Client connected. Total: #{$state[:clients].size}"
      # Send current state immediately
      ws.send($state.to_json)
    end

    ws.on :message do |event|
      # Handle incoming messages (Vote, Admin commands)
      # For now, just echo or log
      p [:message, event.data]
    end

    ws.on :close do |event|
      $state[:clients].delete(ws)
      puts "Client disconnected. Total: #{$state[:clients].size}"
      ws = nil
    end

    # Return async rack response
    ws.rack_response
  else
    "This is a WebSocket endpoint."
  end
end
